package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	k "asteria/internal/knowledge"
	"asteria/internal/transport"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func addServerCommands(root *cobra.Command, a *application) {
	var mcpScopes []string
	mcpCommand := &cobra.Command{Use: "mcp", Short: "以 stdio 启动 MCP 服务（Agent 身份）", RunE: func(cmd *cobra.Command, args []string) error {
		actor := k.Actor{ID: "mcp", Admin: len(mcpScopes) == 0, Grants: scopes(mcpScopes)}
		return transport.MCP(a.service, actor).Run(cmd.Context(), &mcp.StdioTransport{})
	}}
	mcpCommand.Flags().StringSliceVar(&mcpScopes, "grant", nil, "限制 MCP 可访问的 library 或 library/domain")
	root.AddCommand(mcpCommand)

	var address, accessFile string
	serve := &cobra.Command{Use: "serve", Short: "启动 HTTP API 与远程 MCP", RunE: func(cmd *cobra.Command, args []string) error {
		access := transport.Access{LocalOnly: true}
		if accessFile != "" {
			if err := load(accessFile, &access); err != nil {
				return err
			}
		}
		if token := os.Getenv("ASTERIA_API_TOKEN"); token != "" {
			access.Credentials = append(access.Credentials, transport.Credential{Token: token, Actor: k.Actor{ID: "api-agent", Admin: true}})
		}
		if token := os.Getenv("ASTERIA_REVIEW_TOKEN"); token != "" {
			access.Credentials = append(access.Credentials, transport.Credential{Token: token, Actor: k.Actor{ID: "api-reviewer", Admin: true, Human: true}})
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if len(access.Credentials) == 0 && !(host == "localhost" || (ip != nil && ip.IsLoopback())) {
			return fmt.Errorf("a non-loopback listener requires configured access credentials")
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return err
		}
		server := &http.Server{Handler: transport.HTTP(a.service, access), ReadHeaderTimeout: 5 * time.Second}
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		fmt.Fprintf(a.errOut, "Asteria listening on http://%s\n", listener.Addr())
		select {
		case err := <-done:
			if err == http.ErrServerClosed {
				return nil
			}
			return err
		case <-cmd.Context().Done():
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return server.Shutdown(ctx)
		}
	}}
	serve.Flags().StringVar(&address, "addr", "127.0.0.1:8080", "监听地址")
	serve.Flags().StringVar(&accessFile, "access", "", "令牌与角色的 JSON 配置")
	root.AddCommand(serve)
}
