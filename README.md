# feature-flag-config-engine-go

可本地运行的特性开关与动态配置求值引擎。支持多层配置来源合并、运行时热更新、
按请求属性的确定性灰度求值，并提供发布前试算（dry-run）与发布后回滚的兜底能力，
所有拒绝路径都带可区分的原因码。

## 能力概览

- **多层来源合并**：`FileSource`（JSON 文件）、`MapSource`（内存注入，并发安全），
  或任意实现 `Source` 接口的自定义来源。
- **热更新**：`Engine.Reload()` 重新加载全部来源，合并成功后通过单次原子写切换
  快照；合并失败时旧快照继续生效。
- **发布前试算**：`Engine.DryRun(candidate...)` 用候选来源组合按正式合并规则
  试算一遍，输出新增/删除/变更及灰度命中范围走向，不触碰线上生效配置。
- **发布后回滚**：每次成功发布记入历史（默认保留 32 条，可配置），
  `Engine.Rollback(version)` 可把线上整体切回历史中的某一版。
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
| `UNKNOWN_VERSION` | 回滚目标版本不在历史中（未发布过或已被淘汰） |

## 发布前试算（DryRun）

`Engine.DryRun(candidate ...Source)` 拿一份候选来源组合（`MapSource` 注入的候选
内容、`FileSource` 指向的候选文件，或任意组合）试算一遍：

```go
report, err := e.DryRun(candidateSrc)
if err != nil {
    // 与正式合并完全一致的可区分原因：SOURCE_MISSING / TYPE_MISMATCH /
    // INVALID_STRUCTURE / FORBIDDEN_OVERRIDE，绝不静默给默认结果
}
for _, c := range report.Changes {
    // c.Kind: added / removed / changed
    // c.RolloutShift: expanded / shrunk / unchanged（仅灰度键）
}
```

- 试算复用与正式发布**同一个 `Merge`**：优先级、锁定键、校验、拒绝原因语义
  完全一致，不另起一套规则。
- 试算**只读**当前快照：不换快照、不推进版本号、不记历史，试算期间线上
  读写照常进行，并发安全。
- 报告含 `BaseVersion`（对比基准）、`CandidateVersion`（若发布会用的版本号）
  和按 key 排序的变化列表；`Added()/Removed()/Changed()` 提供分类视图。
- 灰度走向：分桶阈值随 `percentage` 单调，`expanded` 表示原命中用户保持命中，
  `shrunk` 表示部分原命中用户退出。

## 发布后回滚（Rollback）

- 每次**成功的发布**（`New` 首次加载与每次 `Reload` 成功）都会作为一条历史
  记录保留；失败的发布不产生任何记录。
- 历史条数上限默认 `DefaultHistoryCapacity = 32`，可通过
  `NewWithOptions(Options{HistoryCapacity: n}, sources...)` 自行指定（<= 0
  回落到默认值）。容量满时**淘汰最旧的一条**；淘汰只影响历史列表，
  绝不影响当前生效版本。
- `Engine.Rollback(version)` 把线上整体切回历史中的某一版：恢复快照内容
  与目标版本**完全一致**，同样的属性输入求值得到同样的结论（含灰度命中与
  分桶结果）。为保持版本号单调递增，切回会分配一个**新的版本号**并作为一次
  成功发布记入历史。
- 目标版本不在历史中时按 `UNKNOWN_VERSION` 拒绝，当前生效版本不受影响。
- `Engine.History()` 返回当前保留的历史（按版本升序的副本）。

## 兼容范围

- **老配置不受影响**：只配 `default` + `rollout` 比例、没有新字段的旧配置
  文件与 `MapSource` 定义照常加载，合并与求值语义不变。
- **现有接口不受影响**：`New`、`Reload`、`Evaluate`、`Snapshot` 签名与行为
  保持不变；`New` 等价于 `NewWithOptions` 使用默认历史容量。
- 试算与回滚是**纯新增**能力，不改变任何既有代码路径的语义。

## 兼容与回退规则

- **更新失败回退**：`Reload()` 失败返回带原因码的错误，旧快照原样保留、版本号
  不前进，所有并发读取不受影响。
- **无静默默认值**：配置缺失/非法不会回退到任何内置默认值；只有求值阶段
  `UNKNOWN_FLAG` / `INVALID_ATTRIBUTE` 这类错误会显式返回给调用方决策。
- **灰度兼容性**：分桶只依赖 `(flagKey, 属性值)` 的哈希，与进程、时间、顺序无关；
  调整 `percentage` 只改变阈值，单调扩大比例时原命中用户保持命中。
- **并发一致性**：快照是不可变对象，通过 `atomic.Pointer` 整体切换；读者要么看到
  完整的旧快照、要么看到完整的新快照。所有发布路径（`Reload`/`Rollback`）由同一把
  互斥锁序列化，版本号分配、合并、切换、历史记录在同一临界区完成，版本号单调递增，
  任何时刻读到的都是一个完整版本，绝不出现半新半旧。

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
- `TestDryRun*`：试算报告（新增/删除/变更/灰度走向）、非法候选按可区分原因
  拒绝、试算不影响线上生效配置；
- `TestRollback*`：回滚后灰度结论与目标版本完全一致、未知版本拒绝；
- `TestHistoryCapacityEviction`：历史容量淘汰最旧记录且不影响当前版本；
- `TestConcurrentReadWriteConsistency` /
  `TestConcurrentReloadAndRollbackConsistency`：并发读写、并发发布+回滚下的
  快照一致性与版本单调性（`-race`）；
- `TestMerge*` / `TestFileSource*`：优先级、锁定键、来源缺失、类型不匹配、结构非法；
- `TestEvalRolloutDeterministic` / `TestEvalRolloutDistribution`：灰度稳定性
  （重复求值 + 模拟重启）与分布合理性。
