# feature-flag-config-engine-go

可本地运行的特性开关与动态配置求值引擎。支持多层配置来源合并、运行时热更新、
按请求属性的确定性灰度求值、**发布前试算**与**发布后回滚**，所有拒绝路径都带
可区分的原因码。

## 能力概览

- **多层来源合并**：`FileSource`（JSON 文件）、`MapSource`（内存注入，并发安全）、
  `ContentSource`（直接给 JSON 候选文本），或任意实现 `Source` 接口的自定义来源。
- **热更新**：`Engine.Reload()` 重新加载全部来源，合并成功后通过单次原子写切换
  快照；合并失败时旧快照继续生效。
- **发布前试算**：`Engine.Simulate()` 拿一份候选来源组合算出"假如真发出去"的结果——
  新增/删除/修改/不变的键，以及灰度范围走向与给定样本的命中变化；**只出结论，
  不动线上**，试算期间线上读写照常。
- **正式发布**：`Engine.Publish()` 用候选来源组合完成一次发布，原子切换并记入历史。
- **发布后回滚**：`Engine.Rollback(version)` 把线上整体切回历史中某一版的内容，
  单次原子写提交，回滚后同样的属性输入得到与该版当初发布时完全一致的结论（含灰度命中）。
- **灰度求值**：按声明属性（如 `user_id`）做 FNV-1a 哈希分桶（0..9999），
  同一输入在重复求值与进程重启后命中同一结果。
- **可解释结论**：每次求值返回 `Decision{Value, Reason, Bucket, Version}`，
  拒绝返回 `*engine.Error{Reason, Message}`。

## 来源优先级与合并规则

1. 每个来源声明 `Priority()`，**数值越大优先级越高**；同名键由高优先级来源覆盖
   低优先级来源（`TestMergePriorityOverride`）。
2. 低优先级来源中 `LockOverride=true` 的键被锁定，任何更高优先级来源再定义该键
   视为越权覆盖，整体合并以 `FORBIDDEN_OVERRIDE` 拒绝。
3. 合并是**全有或全无**：任一来源加载失败或任何一条定义非法，整个新快照被丢弃，
   旧快照继续生效（不存在部分生效的中间态）。

## 拒绝原因码（不静默回退默认值的场景）

| Reason | 触发场景 |
|---|---|
| `SOURCE_MISSING` | 来源不可读（文件缺失、注入错误等） |
| `TYPE_MISMATCH` | 默认值类型与声明 `type` 不符 |
| `INVALID_STRUCTURE` | 结构非法：缺 key、未知类型、rollout 越界（percentage 不在 0-100）、非 bool 开关配灰度、同来源键重复 |
| `FORBIDDEN_OVERRIDE` | 高优先级来源试图覆盖被锁定的键 |
| `UNKNOWN_FLAG` | 求值的开关在当前快照中不存在 |
| `INVALID_ATTRIBUTE` | 灰度所需属性缺失或为空 |
| `VERSION_NOT_FOUND` | 回滚目标版本不在发布历史中（已被淘汰或从未发布） |

## 发布前试算（Simulate）

`Simulate(opts, sources...)` 接收一份**候选来源组合**（即发布后将生效的完整来源集），
复用与正式发布完全相同的 `Merge` 规则——优先级、锁定键、来源缺失/类型不符/结构非法/
越权覆盖的拒绝原因一一对应，候选有问题时直接返回带原因码的错误，**绝不静默给默认结果**。

试算产出 `SimulationReport`：

- `Changes`：按 key 排序的逐键结论，`Kind` 为 `added` / `deleted` / `modified` /
  `unchanged`，并分别汇总到 `AddedKeys` / `DeletedKeys` / `ModifiedKeys` / `UnchangedKeys`；
- 对发生变化的 bool 开关，`FlagChange.Rollout` 给出灰度走向
  （`range-expanded` / `range-shrunk` / `attribute-changed` /
  `rollout-added` / `rollout-removed`）及 `Probes`：给定样本属性在变化前后的
  命中对比（miss→hit 还是 hit→miss）。

试算**只读取**当前快照做对比，不修改当前快照、不前进版本号、不占用发布历史，
试算期间线上读写照常进行。

```go
candidate := &engine.ContentSource{
    SrcName: "candidate", SrcPriority: 0,
    Content: `{"flags":[{"key":"new-checkout","type":"bool","default":false,
        "rollout":{"attribute":"user_id","percentage":75}}]}`,
}
rep, err := eng.Simulate(engine.SimulationOptions{
    Probes: map[string][]string{"user_id": {"alice", "bob"}},
}, candidate)
// rep.ModifiedKeys / rep.Changes[i].Rollout.Direction / .Probes 即发布影响
```

`ContentSource` 的内容格式与 `FileSource` 完全一致，二者共用同一条解析与校验路径，
因此"候选内容读不到/类型对不上/结构不合法"的拒绝原因与文件来源一致。

## 发布、历史与回滚（Publish / History / Rollback）

- 每次**成功的发布**都会追加一条 `HistoryEntry{Version, Snapshot, PublishedAt}`，
  包括引擎首次加载。`Reload()` 与 `Publish()` 的成功结果同样入历史。
- 历史条数有上限：默认 `DefaultHistoryLimit = 20`；`NewWithOptions(Options{
  HistoryLimit: n}, ...)` 可自定义（`n <= 0` 时取默认值）。条数满后淘汰**最旧**
  的一条；淘汰只动历史切片，不影响当前正在生效的版本，也不改动其余历史记录。
- `Rollback(version)` 在历史中找到目标版本，用**同一份开关表内容**生成一个新版本
  并单次原子写提交。回滚产物对灰度求值是确定性的：相同属性输入与该版本当初发布时
  结论完全一致（分桶只依赖 `(flagKey, 属性值)`）。回滚成功本身也作为一次新发布
  追加历史；目标版本已被淘汰或不存在时返回 `VERSION_NOT_FOUND`。
- 发布与回滚在引擎内串行执行（版本号分配顺序与提交顺序一致，读者观察到的版本号
  单调），而读取始终走 `atomic.Pointer` 无锁路径——任何时刻读到的都是完整版本，
  不存在半新半旧。

```go
eng, _ := engine.NewWithOptions(engine.Options{HistoryLimit: 50}, fileSrc)
if err := eng.Publish(candidate); err != nil { /* 原因码见上表，线上未受影响 */ }
for _, h := range eng.History() { fmt.Println(h.Version, h.PublishedAt) }
snap, err := eng.Rollback(1) // 整体切回 v1 的内容（提交为一个新版本）
```

> 说明：回滚后快照的版本号是新的单调递增值（保证并发读者版本单调），但其**配置内容
> 与灰度命中结果**与目标历史版本完全等价，这正是"和当初那一版刚发布时一致"的保证。

## 兼容与回退规则

- **老配置与现有接口不受影响**：原来只配灰度比例（无 `lock_override` 等字段）的
  JSON 配置、`FileSource` / `MapSource`、`New` / `Reload` / `Evaluate` / `Eval`
  等现有调用方式全部保持原行为（见 `TestLegacyRolloutOnlyConfigCompat` 及全部
  原有测试，均不回归）。新增能力以附加 API（`NewWithOptions` / `Simulate` /
  `Publish` / `Rollback` / `History` / `ContentSource`）提供。
- **更新失败回退**：`Reload()` / `Publish()` / `Simulate()` 失败均返回带原因码的
  错误，线上快照原样保留、版本号不前进，所有并发读取不受影响。
- **回滚一致性**：回滚用的是历史版本的完整开关表，求值/锁定/灰度语义不另起炉灶；
  被回滚版本中"只配灰度比例"的老开关，回滚后命中结果与当初一致。
- **无静默默认值**：配置缺失/非法不会回退到任何内置默认值；只有求值阶段
  `UNKNOWN_FLAG` / `INVALID_ATTRIBUTE` 这类错误会显式返回给调用方决策。
- **灰度兼容性**：分桶只依赖 `(flagKey, 属性值)` 的哈希，与进程、时间、顺序无关；
  调整 `percentage` 只改变阈值，单调扩大比例时原命中用户保持命中。
- **并发一致性**：快照是不可变对象，通过 `atomic.Pointer` 整体切换；读者要么看到
  完整的旧快照、要么看到完整的新快照，版本号单调递增。发布/回滚串行提交，
  历史淘汰不触碰当前生效版本。

## 配置文件格式（FileSource）

```json
{
  "flags": [
    {
      "key": "new-checkout",
      "type": "bool",
      "default": false,
      "rollout": {"attribute": "user_id", "percentage": 50},
      "lock_override": false
    },
    {"key": "banner-text", "type": "string", "default": "hello"},
    {"key": "limits", "type": "json", "default": {"qps": 100}}
  ]
}
```

`type` 支持 `bool` / `string` / `number` / `json`；`rollout` 仅允许用于 `bool`。

## 本地验证

```bash
# 运行全部单测（含竞态检测）；日志打印每次求值的输入与判定依据
go test ./engine/ -race -v

# 运行端到端演示：来源合并、热更新、失败回退
go run ./cmd/demo
```

测试覆盖：

- `TestHotUpdate` / `TestFailedReloadKeepsOldSnapshot`：热更新与失败回退；
- `TestConcurrentReadWriteConsistency`：8 读 4 写并发下快照一致性与版本单调性（`-race`）；
- `TestMerge*` / `TestFileSource*`：优先级、锁定键、来源缺失、类型不匹配、结构非法；
- `TestEvalRolloutDeterministic` / `TestEvalRolloutDistribution`：灰度稳定性
  （重复求值 + 模拟重启）与分布合理性；
- `TestContentSource*`：候选内容加载与空内容/解析/类型/结构四类可区分拒绝；
- `TestSimulateDiff` / `TestSimulateRolloutTrend`：试算的新增/删除/修改/不变结论、
  灰度走向与探针命中，以及试算不触碰线上；
- `TestSimulateRejections`：试算沿用锁定键越权、来源缺失、类型不匹配等拒绝原因；
- `TestPublishHistoryAndRollback` / `TestRollbackRestoresFullSet`：发布入历史、
  回滚后 200 个用户灰度命中零偏差、整体开关集恢复、`VERSION_NOT_FOUND`；
- `TestHistoryEviction` / `TestDefaultHistoryLimit`：历史容量上限与最旧淘汰，
  淘汰不影响当前生效版本；
- `TestConcurrentPublishRollbackRead`：8 读者 + 4 写者并发发布/回滚/求值下
  快照完整一致、版本单调（`-race`）；
- `TestLegacyRolloutOnlyConfigCompat`：只配灰度比例的老配置在加载/试算/求值下
  行为保持不变。

所有测试日志均打印每次判定的输入与依据（属性输入、分桶、命中结论或拒绝原因码），
可用 `go test ./engine/ -race -v` 查看。
