# feature-flag-config-engine-go

可本地运行的特性开关与动态配置求值引擎。支持多层配置来源合并、运行时热更新、
按请求属性的确定性灰度求值，所有拒绝路径都带可区分的原因码。

## 能力概览

- **多层来源合并**：`FileSource`（JSON 文件）、`MapSource`（内存注入，并发安全），
  或任意实现 `Source` 接口的自定义来源。
- **热更新**：`Engine.Reload()` 重新加载全部来源，合并成功后通过单次原子写切换
  快照；合并失败时旧快照继续生效。
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

## 兼容与回退规则

- **更新失败回退**：`Reload()` 失败返回带原因码的错误，旧快照原样保留、版本号
  不前进，所有并发读取不受影响。
- **无静默默认值**：配置缺失/非法不会回退到任何内置默认值；只有求值阶段
  `UNKNOWN_FLAG` / `INVALID_ATTRIBUTE` 这类错误会显式返回给调用方决策。
- **灰度兼容性**：分桶只依赖 `(flagKey, 属性值)` 的哈希，与进程、时间、顺序无关；
  调整 `percentage` 只改变阈值，单调扩大比例时原命中用户保持命中。
- **并发一致性**：快照是不可变对象，通过 `atomic.Pointer` 整体切换；读者要么看到
  完整的旧快照、要么看到完整的新快照，版本号单调递增。

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
  （重复求值 + 模拟重启）与分布合理性。
