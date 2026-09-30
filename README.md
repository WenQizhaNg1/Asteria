# Asteria

由人和 Agent 共同维护的 Markdown 知识引擎原型。Go 实现，CLI、HTTP、MCP 共用操作层；知识保存在本地版本化文件中，模型可替换。

| 文档                                            | 阅读目的                                     |
| ----------------------------------------------- | -------------------------------------------- |
| 本页                                            | 构建、体验、模型接入与接口使用               |
| [系统设计](Asteria-1.0-System-Design.md)        | 知识表达、四个操作、权限与审核规则           |
| [技术选型](Asteria-1.0-Technology-Selection.md) | 当前依赖、代码边界、存储方式及云原生演进条件 |
| [验收记录](docs/verification.md)                | 已验证行为、审查修复与实际限制               |

## 快速体验

需要 Go 1.25+ 和 Linux / macOS。没有模型也能跑通下面的流程：

```sh
go build -buildvcs=false -trimpath -o bin/asteria ./cmd/asteria
./bin/asteria --offline init --schema examples/traffic.yaml
./bin/asteria --offline init --schema examples/tourism.yaml
./bin/asteria --offline write --library traffic --domain parking --source demo://parking --file examples/parking.md
./bin/asteria --offline write --library tourism --domain shuttle --source demo://shuttle --file examples/shuttle.md
./bin/asteria --offline query '停车分流与接驳运力有哪些约束？' --scope traffic,tourism
```

也可执行 `make demo`。示例是人工演示资料，不是真实交通规范。默认目录为 `./data`，通过 `--data PATH` 或 `ASTERIA_DATA` 修改。命令输出 JSON；`./bin/asteria --help` 查看完整命令。

## 修订与审核

```sh
./bin/asteria browse traffic/parking
./bin/asteria revise traffic/ENTRY_ID --instruction '补充失效条件' --file examples/parking-revised.md
./bin/asteria proposal show PROPOSAL_ID
./bin/asteria proposal approve PROPOSAL_ID --version EXACT_PROPOSAL_VERSION
```

将占位符替换为前一步的返回值。新资料可在校验后自动保存；改写、合并、撤回和结构调整需要审核。`proposal show` 显示完整前后文件，批准绑定确切版本；基准变化后应重新提出修改。拒绝使用 `proposal reject ID --version VERSION --reason 理由`。

常用管理命令：

```sh
./bin/asteria read traffic/ENTRY_ID --markdown
./bin/asteria read traffic/ENTRY_ID --version FILE_VERSION
./bin/asteria history
./bin/asteria reindex
./bin/asteria export ./working-copy
```

请通过接口修改知识，完整备份需保留整个数据目录；导出只包含当前工作副本。恢复旧内容使用 `revise TARGET --instruction '恢复历史内容' --restore VERSION`。已有领域配置通过 `revise schema:LIBRARY --schema FILE --base COMMIT --instruction '调整领域'` 提案，不能用重复初始化覆盖。

关系核查使用 `revise TARGET --instruction '人工核查' --review-relation ID --status verified --note '核查依据及适用条件'`，再批准提案；可用 `--evidence refs.json` 更新版本化依据。`rejected` 表示否定，必须保留理由。

## 模型接入

维护 OpenRouter、DeepSeek、Qwen、Kimi 和 GLM，业务层统一通过 `core.Model` 调用。先填写账号可用的模型 ID 和 API Key：

```sh
export ASTERIA_MODEL_PROVIDER='deepseek'
export ASTERIA_MODEL_NAME='your-model-id'
export ASTERIA_MODEL_KEY='your-api-key'
./bin/asteria query '停车排队和接驳候车有哪些共同约束？' --scope traffic,tourism
./bin/asteria reflect --scope traffic,tourism --goal '发现分流与接驳之间的候选联系'
```

| 提供商与官方接口说明                                                                              | `ASTERIA_MODEL_PROVIDER` | 默认 API 地址                                       |
| ------------------------------------------------------------------------------------------------- | ------------------------ | --------------------------------------------------- |
| [OpenRouter](https://openrouter.ai/docs/api/reference/overview)                                   | `openrouter`             | `https://openrouter.ai/api/v1`                      |
| [DeepSeek](https://api-docs.deepseek.com/guides/json_mode)                                        | `deepseek`               | `https://api.deepseek.com`                          |
| [Qwen / 阿里云百炼](https://help.aliyun.com/zh/model-studio/qwen-api-via-openai-chat-completions) | `qwen`                   | `https://dashscope.aliyuncs.com/compatible-mode/v1` |
| [Kimi / Moonshot](https://platform.moonshot.cn/docs/api/chat)                                     | `kimi`                   | `https://api.moonshot.cn/v1`                        |
| [GLM / 智谱](https://docs.bigmodel.cn/cn/guide/capabilities/struct-output)                        | `glm`                    | `https://open.bigmodel.cn/api/paas/v4`              |

`ASTERIA_MODEL_URL` 可覆盖默认地址，支持区域端点、Qwen 业务空间域名或代理；请使用对应地域和服务的 Key。地址可包含 `/chat/completions`，否则自动追加。模型名称必须显式配置，OpenRouter 通常使用 `厂商/模型` ID。

选择支持 `response_format: json_object` 的文本模型。OpenRouter 设置 `provider.require_parameters=true`；Qwen 设置 `enable_thinking=false`，兼容仅在非思考模式支持 JSON 的模型；Kimi 使用 `max_completion_tokens`，其余使用 `max_tokens`。`ASTERIA_MODEL_MAX_TOKENS` 默认 `4096`，`ASTERIA_MODEL_TIMEOUT` 默认 `90s`；思考或长输出模型可能需要提高这些值，操作自身的 `--timeout` 仍限制整个流程。

本地或其他兼容服务可设置 `ASTERIA_MODEL_PROVIDER=compatible`，并指定 URL 与模型，Key 可选。省略 provider 时仍按此方式处理，兼容原来的配置：

```sh
export ASTERIA_MODEL_PROVIDER='compatible'
export ASTERIA_MODEL_URL='http://127.0.0.1:11434/v1'
export ASTERIA_MODEL_NAME='your-local-model'
unset ASTERIA_MODEL_KEY
```

未配置任何模型变量时使用确定性操作；配置不完整会报错，`--offline` 可忽略全部模型配置。五家适配已通过本地 HTTP 协议测试，尚未使用真实凭据联调；具体模型的兼容性与效果需实测。

| 操作    | 不配置模型                                 | 配置模型                       |
| ------- | ------------------------------------------ | ------------------------------ |
| query   | 关键词与引用检索，返回原文摘录             | 在预算内导航并生成带出处的回答 |
| write   | 按明确的库与领域保存原文                   | 整理归属、内容或提出合并       |
| revise  | 显式替换正文、恢复、撤回、改领域或人工核查 | 根据意图和依据提出正文修订     |
| reflect | 报告确定性检查结果                         | 提出候选洞察、正文或领域调整   |

`query` / `reflect` 可用 `--max-steps`、`--max-reads`、`--max-bytes`、`--timeout` 限制探索，内省用 `--max-changes` 限制提案文件数。运行轨迹通过 `trace ID` 查看。模型输出始终经过同一套知识规则，当前流程直接用 Go 实现。

## HTTP 与 MCP

```sh
./bin/asteria serve
curl http://127.0.0.1:8080/v1/query -H 'Content-Type: application/json' -d '{"question":"停车分流有哪些条件？","scope":[{"library_id":"traffic"}]}'
```

未配置令牌时只允许回环访问，身份为 Agent，人工审核使用 CLI。远程服务设置 `ASTERIA_API_TOKEN`；需要远程审核时另设不同的 `ASTERIA_REVIEW_TOKEN`，客户端发送 `Authorization: Bearer TOKEN`。细分授权用 `serve --access FILE`，参考[访问配置示例](examples/access.example.json)，替换其中占位令牌。

| 路径                                               | 行为                                                             |
| -------------------------------------------------- | ---------------------------------------------------------------- |
| POST /v1/query、/v1/write、/v1/revise、/v1/reflect | 四个核心操作，请求字段见 [core](internal/core) 中的 Request 类型 |
| GET /v1/libraries；POST /v1/libraries              | 列出授权库；人类管理员初始化库                                   |
| GET /v1/browse?library=ID&domain=ID                | 浏览领域                                                         |
| GET /v1/entries/LIBRARY/ENTRY?version=VERSION      | 读取当前或历史版本                                               |
| GET /v1/proposals、/v1/proposals/ID                | 提案列表和完整差异                                               |
| POST /v1/proposals/ID/approve、/reject             | 请求包含 version；拒绝另含 reason                                |
| GET /v1/history、/v1/traces/ID                     | 操作历史与授权轨迹                                               |
| POST /v1/reindex                                   | 请求 `{}`，重建派生索引                                          |
| /mcp；GET /healthz                                 | 远程 MCP；存活检查                                               |

API 不接受请求自行声明身份。常见错误：400 参数或依据无效、401 未认证、403 无权限、404 无可见目标、409 版本冲突。

stdio MCP 客户端配置：

```json
{
  "mcpServers": {
    "asteria": {
      "command": "/absolute/path/to/bin/asteria",
      "args": [
        "--data",
        "/absolute/path/to/data",
        "mcp",
        "--grant",
        "traffic,tourism"
      ]
    }
  }
}
```

提供 query、write、revise、reflect、browse、read、libraries。MCP 始终使用 Agent 身份，不提供审批工具；远程连接使用 `/mcp` 和相应 Bearer 令牌。

## 部署与验证

```sh
go test -race ./...
go vet ./...
```

容器部署使用 `docker compose up --build`，先设置上述 API 令牌。服务使用独立命名卷，非 root 运行；通过人类管理员 API 凭据初始化库。当前环境没有 Docker，镜像构建与 Compose 启动尚未实测。

原型采用完整快照、单写锁与内存词法检索，支持单机，不支持多机共享写入。真实资料上的引用质量和洞察实用性尚需评估。本轮未做性能压测；功能证据与审查结论见[验收记录](docs/verification.md)。
