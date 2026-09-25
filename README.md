# Feature Flag & Dynamic Config Engine (Go)

一个可在本地运行的特性开关 / 动态配置求值引擎，提供：

- **多层来源合并**：确定性优先级，每个结论都可追溯到获胜来源；
- **运行时热更新**：无锁原子快照切换，读多写少下永不读到"半生效"状态；更新失败旧配置继续生效；
- **按属性灰度求值**：有序规则首命中 + 稳定哈希分桶，同输入在重复求值与进程重启后结论一致；
- **严格拒绝而非静默回退**：来源缺失、类型不符、结构非法、非法属性、越权覆盖均以**可区分的错误码**拒绝。

## 目录结构

```
internal/domain   核心模型、错误码（Reason）与可解释 Decision/Trace
internal/source   配置来源：File（每次读取文件，支持热更新）、Static（内存）
internal/parse    严格 JSON 解析：未知字段/重复键/类型/结构校验
internal/merge    多层合并与来源溯源（provenance）
internal/engine   原子快照、热更新、规则匹配、稳定分桶、类型化属性读取
cmd/demo          本地可运行示例
testdata          base / override 两层合法配置及两份非法配置样例
```

## 配置格式

```json
{
  "flags": {
    "checkout_new": {
      "default_variation": "off",
      "allow_override": ["checkout_new"],
      "rules": [
        {"name": "staff",
         "attribute": "group", "operator": "in", "values": ["staff", "qa"],
         "percentage": 100, "variation": "on"},
        {"name": "canary",
         "attribute": "region", "operator": "eq", "values": ["cn"],
         "percentage": 10, "bucket_by": "user_id", "variation": "on"}
      ]
    }
  },
  "properties": {
    "checkout_timeout_ms": {"type": "number", "value": 3000},
    "theme":               {"type": "string", "value": "light"}
  }
}
```

- `operator`：`eq` `ne` `in` `not_in` `gt` `lt` `ge` `le` `starts_with` `ends_with`；
  `gt/lt/ge/le` 按数值比较，属性不可解析为数字时以 `invalid_attribute` 拒绝。
- `percentage`：0..100，缺省视为 100；小于 100 **必须**声明 `bucket_by`，否则整份配置以
  `illegal_structure` 拒绝。
- `variation`：`on/true/enabled` 解释为布尔 `true`，其余为 `false`。
- 属性只接受 JSON 标量（string / bool / number / null）；对象与数组一律 `invalid_attribute`。

## 来源优先级与合并规则（可解释）

1. 构造引擎时按切片顺序登记来源，**下标 0 优先级最高**。
2. flag / property 都按**整键（whole-key）**取值：第一个定义该键的来源整体获胜，
   不做跨层字段级深合并——避免"这条规则来自 A、默认值来自 B"这类无法解释的拼接。
3. 合并结果记录 provenance（`Decision.Source`、属性读取返回的 source），
   每个判定都能回答"这是哪一层给的"。
4. `testdata/override.json` 整体覆盖 `testdata/base.json` 中同名的
   `checkout_new` 与 `theme`；`legacy_paywall`、`checkout_timeout_ms` 等仅在低层定义的键照常生效。

## 兼容与回退规则

- **唯一允许的"回退"是 flag 自己声明的 `default_variation`**：规则都未命中时使用它，
  trace 中记录 `reason=default`。未声明默认值的 flag 配置本身即非法，加载即拒绝。
- 以下情况**绝不静默回退**，而是返回携带 `Reason`、`Source`、`Path` 的 `domain.Error`：

  | Reason | 触发场景 |
  |---|---|
  | `source_unreadable` | 文件不存在 / IO 失败 |
  | `invalid_json` | JSON 语法错误、空文档、尾部多余数据 |
  | `type_mismatch` | 值的 JSON 类型与声明类型/期望结构不符（含精确定位路径） |
  | `illegal_structure` | 未知字段、非法 operator、percentage 越界、缺 bucket_by、缺 default_variation 等 |
  | `duplicate_key` | 同一 JSON 对象内键重复 |
  | `unknown_flag` / `unknown_property` | 任何来源都未定义该键 |
  | `invalid_attribute` | 请求属性含不支持的类型，或数值算子收到非数字 |
  | `override_denied` | 求值时覆盖未在该 flag 的 `allow_override` 中授权的键 |
  | `rule_illegal` | 规则无法求值 |

- 类型化属性读取（`GetBool/GetNumber/GetString`）遇到声明类型不符时返回 `type_mismatch`，
  不做隐式转换。

## 热更新与一致性

- 内存中只保存**不可变快照**（`atomic.Pointer[snapshot]`）。
- `Reload()` 先完整执行：读全部来源 → 严格解析 → 合并；**全部成功后才做一次原子 Store**。
  因此并发读只能看到旧快照或新快照，不会看到部分来源生效的中间态（`TestConcurrentReadersSeeConsistentSnapshots`）。
- 任一步骤失败则不交换指针，旧快照继续服务；错误带 reason/source/path 可定位
  （`TestFailedReloadKeepsOldSnapshot`）。

## 灰度稳定性

- 命中条件后做分桶判定：`FNV-1a32(flagKey, ruleName, bucketByValue) % 100 < percentage`。
- 哈希是纯函数、无随机数、无内存分配表：
  同输入重复求值一致；重建引擎（模拟进程重启）后仍然一致
  （`TestPercentageStableAcrossRepeatsAndEngines`）。
- 加入 flag/rule 名做命名空间，避免不同开关的灰度人群完全重合。
- 条件命中但 `bucket_by` 属性缺失时，该规则视为未命中并继续向下匹配（trace 记录原因）。

## 求值时覆盖的授权模型

`EvaluateFlag(key, attrs, overrides)` 中的 `overrides`：

- 仅当 `key` 出现在**该 flag 自身**的 `allow_override` 中才允许 `{key: bool}` 强制结果；
- 对其他键（哪怕是别的合法 flag）的覆盖一律 `override_denied`；
- 覆盖值非布尔时返回 `invalid_attribute`。

## 本地验证

```bash
go build ./...
go vet ./...
go test -race ./... -v        # 测试日志打印输入 attrs 与每步判定依据
go run ./cmd/demo             # 两层合并、灰度分桶、热更新、非法层拒绝的完整演示
```

demo 输出会展示：每个用例的 variation / reason / 获胜来源、属性来源溯源、
reload 后版本号递增，以及非法配置层被精确拒绝
（`type_mismatch source=bad path=properties.checkout_timeout_ms.value ...`）。
