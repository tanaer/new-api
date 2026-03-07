# 供应商「一键更新渠道」改造 Spec（草案）

## 1. 背景与问题

当前供应商管理已经支持：

1. 从上游 `/api/pricing` 采集分组倍率、支持模型、端点类型。
2. 自动生成/回填分组 API Key。
3. 同步倍率到系统 `GroupRatio`。
4. 按供应商更新渠道（单供应商仅更新，不自动创建；全量同步会自动创建）。

但现有行为与期望存在关键偏差：

1. 「采集+生成密钥」和「一键同步」没有完整统一为一次增改删闭环。
2. 单供应商同步默认不创建缺失渠道，导致仍需额外手工批量创建。
3. 分组端点类型目前保存为单值，且推导逻辑偏粗，未充分利用 `/api/pricing` 的 `supported_endpoint_types`。
4. `saveSupplierGroupToken` 在 `local_group` 为空时会写入 `upstream_group`，可能把上游分组名带入本地倍率，造成“本地分组被上游污染”。
5. 前端命名与交互以“创建”为主，不符合“更新并对齐”的目标。

## 2. 目标（Goal）

围绕单供应商操作，统一成「一键更新渠道」流程，一次完成：

1. 采集上游最新分组信息（倍率、模型 ID、supported endpoint types）。
2. 自动做一次上游分组 -> 本地分组映射建议（按关键字分类 + 最近倍率）。
3. 生成/复用分组 API Key。
4. 同步倍率到系统（仅限已映射且合法的本地分组）。
5. 自动对本地渠道执行增量对齐：
   - 上游有，本地有 -> 更新；
   - 上游有，本地无 -> 新增；
   - 上游无，本地有 -> 硬删除。
6. 返回结构化诊断信息，便于定位失败步骤和差异。

## 3. 非目标（Non-Goal）

1. 不改动用户端 `/api/pricing` 对外格式。
2. 不改动供应商之外的渠道编辑页核心交互。
3. 不在本次改造中做异步任务编排（先同步执行，确保可观测和可回滚）。

## 4. 关键原则

### 4.1 本地分组主导

1. 不创建任何“上游分组到本地分组配置”的新组名。
2. 自动映射只作用于 `supplier_groups.local_group` 字段，不改系统分组定义。
3. 同步倍率前必须校验 `local_group` 在本地分组集合内；不在则跳过并告警。

### 4.2 映射可复查

1. 自动映射只在“当前无映射”时填充（默认不覆盖人工已设映射）。
2. 映射规则可解释、可复现、可在响应中看到来源和评分信息（用于人工复核）。

### 4.3 同步幂等

同一供应商连续多次执行，在上游无变化时应无额外副作用（除日志时间戳外）。

## 5. 目标交互与命名

## 5.1 文案调整

1. 将「一键创建渠道」统一改名为「一键更新渠道」。
2. 将说明文案从“创建导向”改为“同步对齐导向（增/改/清）”。

## 5.2 交互流程

单供应商分组管理页主按钮：

1. 点击「一键更新渠道」。
2. 后端执行完整流水线。
3. 前端展示结果摘要 + 分步骤详情 + 告警列表。
4. 若存在未映射分组，显式列出并允许人工改映射后再次执行。

## 6. 数据来源与解析规则

上游数据源：`GET {supplier.base_url}/api/pricing`

关键信息：

1. `group_ratio`: 上游分组倍率。
2. `data[].model_name`: 上游模型 ID。
3. `data[].enable_groups`: 模型可用分组。
4. `data[].supported_endpoint_types`: 该模型支持端点类型列表（可能多个）。

解析目标：

1. `group -> ratio`
2. `group -> models[]`（去重后有序）
3. `group -> endpoint_types[]`（去重后有序）
4. `group -> preferred_endpoint_type`（用于确定默认通道类型）

`preferred_endpoint_type` 规则：

1. 若端点列表含 `openai`，优先取 `openai`。
2. 否则取列表第一个。
3. 若列表为空，默认 `openai`。

## 7. 分组自动映射规则

输入：`upstream_group`、`upstream_ratio`、本地分组集合（含倍率）。

步骤：

1. 分类（按上游分组名，忽略大小写）：
   - 包含 `cc` 或 `claude` -> `cc` 类；
   - 包含 `codex` 或 `openai` -> `codex` 类；
   - 包含 `gemini` -> `gemini` 类；
   - 其他 -> 不自动映射。
2. 从本地分组中筛选同类别前缀：
   - `cc*` / `codex*` / `gemini*`。
3. 在候选中按 `|local_ratio - upstream_ratio|` 最小匹配。
4. 若无候选，留空待人工处理。

默认策略：

1. 仅在 `local_group` 为空时自动填充。
2. 非空 `local_group` 视为人工确认，不自动覆盖。

## 8. 「一键更新渠道」后端流水线

建议保留现有路由 `POST /api/supplier/:id/sync_full`，语义升级为“更新渠道”；前端文案改名。可选新增别名路由 `sync_update`。

执行步骤：

1. 采集并解析上游 pricing。
2. `supplier_groups` Upsert：
   - 按 `(supplier_id, upstream_group)` 对齐；
   - 更新倍率、模型 ID 列表、端点类型信息；
   - 新增分组时尝试自动映射本地分组；
   - 标记本次上游存在的分组集合。
3. 密钥阶段：
   - 查询/复用同名 token；
   - 缺失则创建 token 并回填；
   - 不再把 `local_group` 空值回填为 `upstream_group`。
4. 倍率同步阶段：
   - 仅处理“已映射且本地分组合法”的记录；
   - `final_ratio = round3(group_ratio * supplier.markup)`；
   - 同一本地分组多来源时取最大值并告警（安全优先）。
5. 渠道对齐阶段（核心）：
   - 目标集：`local_group` 非空 + `api_key` 非空；
   - 本地存在则更新：`key/models/type/base_url`（必要字段）；
   - 本地不存在则新增渠道；
   - 本地有但目标集无则硬删除。
6. 缓存刷新与同步日志落库。
7. 返回结构化结果。

## 9. 通道类型确定规则

从 `preferred_endpoint_type` 推导 `channel.type`：

1. `anthropic` -> `ChannelTypeAnthropic`
2. `gemini` -> `ChannelTypeGemini`
3. 其他（`openai/openai-response/openai-response-compact/...`）-> `ChannelTypeOpenAI`

补充：

1. 若某组支持多端点，默认优先 `openai`（符合“可多个时默认 openai”）。
2. 同时在 `supplier_groups` 保存完整端点类型列表，便于后续策略升级。

## 10. 数据模型调整建议

当前 `supplier_groups` 已有：

1. `supported_models`（逗号串）
2. `endpoint_type`（单值）

建议增强（兼容式）：

1. 新增 `endpoint_types`（`TEXT`，JSON 数组或逗号串）用于保存完整 `supported_endpoint_types`。
2. 保留 `endpoint_type` 作为首选端点（兼容旧代码）。

说明：

1. 三库兼容下统一使用 `TEXT`，避免数据库专有 JSON 类型依赖。
2. 所有 JSON 编解码使用 `common.Marshal/common.Unmarshal`。

## 11. API 响应（建议）

`POST /api/supplier/:id/sync_full` 返回增强结构（兼容保留 `success/message/warnings`）：

```json
{
  "success": true,
  "message": "更新完成: 分组+2/~5, 渠道+3/~8/-1, 未映射2",
  "details": {
    "groups_total": 12,
    "groups_added": 2,
    "groups_updated": 5,
    "keys_created": 3,
    "keys_reused": 7,
    "keys_failed": 0,
    "ratios_synced": 6,
    "ratios_skipped_invalid_local_group": 1,
    "channels_created": 3,
    "channels_updated": 8,
    "channels_disabled": 1,
    "unmapped_count": 2
  },
  "warnings": [],
  "unmapped_groups": [],
  "steps": [
    { "name": "fetch_pricing", "success": true, "cost_ms": 120 },
    { "name": "sync_groups", "success": true, "cost_ms": 45 },
    { "name": "provision_keys", "success": true, "cost_ms": 380 },
    { "name": "sync_ratios", "success": true, "cost_ms": 18 },
    { "name": "reconcile_channels", "success": true, "cost_ms": 52 }
  ]
}
```

## 12. 兼容与迁移

1. 保持现有接口路径不变，先升级语义和返回详情。
2. 前端按钮文案统一改为「一键更新渠道」。
3. `batch_create_channels` 先保留为兼容入口，但 UI 降级为“高级/手动模式”。
4. 全量接口 `sync_all_full` 后续按同样规则升级（可作为二阶段）。

## 13. 观测与排障

每次执行写一条 `SupplierGroupSyncLog`，建议 `details` 包含：

1. 供应商名称与 ID
2. 关键计数（分组/密钥/倍率/渠道）
3. 失败摘要（阶段 + 原因）
4. 未映射分组列表（截断）

前端展示：

1. 主结论（成功/部分成功/失败）
2. 阶段耗时
3. 可展开告警清单
4. 未映射分组快速入口

## 14. 验收标准（Acceptance Criteria）

1. 点击「一键更新渠道」后，单次请求可完成采集+密钥+模型/端点更新+渠道增改清+倍率同步。
2. 上游新增分组（且可映射）会自动新增本地渠道。
3. 上游移除分组后，本地对应渠道会被硬删除。
4. 不会新增任何“上游命名”的本地系统分组。
5. `local_group` 为空时不会再被自动写成 `upstream_group`。
6. 响应中能明确看到：新增/更新/清除数量、未映射分组、失败阶段。

## 15. 实施清单（建议顺序）

1. 后端修正：
   - 修复 `saveSupplierGroupToken` 的 `local_group` 回填逻辑；
   - 完善 pricing 解析（按组聚合端点类型与模型）；
   - 升级 `sync_full` 为真正增改清；
   - 增强响应与日志结构。
2. 前端改造：
   - 文案改「一键更新渠道」；
   - 同步结果展示改为阶段化诊断；
   - 未映射区保留人工复核入口。
3. 回归验证：
   - 单供应商全流程；
   - 多供应商全量同步；
   - 上游分组增删场景；
   - 本地分组保护场景（不污染）。

## 16. 已确认决策

1. 「清除」语义采用“硬删除渠道”。
2. 对已手动设置的 `local_group` 始终保护，不做自动覆盖。
