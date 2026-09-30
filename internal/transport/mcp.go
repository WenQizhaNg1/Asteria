package transport

import (
	"context"

	"asteria/internal/core"
	k "asteria/internal/knowledge"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func MCP(service *core.Service, actor k.Actor) *mcp.Server {
	actor.Human = false
	server := mcp.NewServer(&mcp.Implementation{Name: "asteria", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "query", Description: "在授权范围内探索知识，返回版本化证据、候选洞察和停止原因；不会修改知识。"},
		func(ctx context.Context, _ *mcp.CallToolRequest, request core.QueryRequest) (*mcp.CallToolResult, core.QueryResult, error) {
			result, err := service.Query(ctx, actor, request)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "write", Description: "保存有来源的 Markdown，自动归类或返回提案；正文合并需要人工审核。"},
		func(ctx context.Context, _ *mcp.CallToolRequest, request core.WriteRequest) (*mcp.CallToolResult, k.CommitResult, error) {
			result, err := service.Write(ctx, actor, request)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "revise", Description: "提出知识或领域修订，保留来源与历史；返回需要人工批准的确切提案。"},
		func(ctx context.Context, _ *mcp.CallToolRequest, request core.ReviseRequest) (*mcp.CallToolResult, k.CommitResult, error) {
			result, err := service.Revise(ctx, actor, request)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "reflect", Description: "在预算内发现有原始证据的候选联系，或提出组织调整；不能自行验证关联。"},
		func(ctx context.Context, _ *mcp.CallToolRequest, request core.ReflectRequest) (*mcp.CallToolResult, core.ReflectResult, error) {
			result, err := service.Reflect(ctx, actor, request)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "browse", Description: "浏览授权领域的子领域、条目和摘要。", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, location k.Scope) (*mcp.CallToolResult, core.Directory, error) {
			result, err := service.Browse(actor, location)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "read", Description: "按稳定 ID 和可选版本读取 Markdown；旧证据仍指向旧版本。", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, ref k.Ref) (*mcp.CallToolResult, k.Entry, error) {
			result, err := service.Read(actor, ref)
			return nil, result, err
		})
	mcp.AddTool(server, &mcp.Tool{Name: "libraries", Description: "列出授权知识库与领域。", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, map[string]any, error) {
			result, err := service.Libraries(actor)
			return nil, map[string]any{"libraries": result}, err
		})
	return server
}
