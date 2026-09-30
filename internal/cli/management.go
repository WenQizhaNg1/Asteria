package cli

import (
	k "asteria/internal/knowledge"
	"github.com/spf13/cobra"
)

func addManagementCommands(root *cobra.Command, a *application) {
	var schemaFile string
	init := &cobra.Command{Use: "init", Short: "从 JSON / YAML 初始化知识库", RunE: func(cmd *cobra.Command, args []string) error {
		var schema k.Schema
		if err := load(schemaFile, &schema); err != nil {
			return err
		}
		snapshot, err := a.service.Init(cmd.Context(), k.LocalActor(), schema)
		if err != nil {
			return err
		}
		return a.print(map[string]string{"commit_id": snapshot.Commit.ID, "library_id": schema.LibraryID}, nil)
	}}
	init.Flags().StringVar(&schemaFile, "schema", "", "领域配置文件")
	_ = init.MarkFlagRequired("schema")
	root.AddCommand(init)

	libraries := &cobra.Command{Use: "libraries", Short: "列出知识库与领域", RunE: func(cmd *cobra.Command, args []string) error {
		return a.print(a.service.Libraries(k.LocalActor()))
	}}
	root.AddCommand(libraries)

	proposal := &cobra.Command{Use: "proposal", Short: "查看与审核确切提案"}
	proposal.AddCommand(&cobra.Command{Use: "list", RunE: func(cmd *cobra.Command, args []string) error {
		return a.print(a.service.Proposals(k.LocalActor()))
	}})
	proposal.AddCommand(&cobra.Command{Use: "show ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.print(a.service.Proposal(k.LocalActor(), args[0]))
	}})
	var approveVersion string
	approve := &cobra.Command{Use: "approve ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.print(a.service.Approve(cmd.Context(), k.LocalActor(), args[0], approveVersion))
	}}
	approve.Flags().StringVar(&approveVersion, "version", "", "proposal show 中的确切 version")
	_ = approve.MarkFlagRequired("version")
	proposal.AddCommand(approve)
	var rejectVersion, rejectReason string
	reject := &cobra.Command{Use: "reject ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.print(a.service.Reject(cmd.Context(), k.LocalActor(), args[0], rejectVersion, rejectReason))
	}}
	reject.Flags().StringVar(&rejectVersion, "version", "", "确切提案版本")
	reject.Flags().StringVar(&rejectReason, "reason", "", "拒绝理由")
	_ = reject.MarkFlagRequired("version")
	proposal.AddCommand(reject)
	root.AddCommand(proposal)

	var readVersion string
	var markdown bool
	read := &cobra.Command{Use: "read TARGET", Args: cobra.ExactArgs(1), Short: "读取条目或历史版本", RunE: func(cmd *cobra.Command, args []string) error {
		entry, err := a.service.Resolve(k.LocalActor(), args[0])
		if err != nil {
			return err
		}
		if readVersion != "" {
			ref := entry.Ref()
			ref.Version = readVersion
			entry, err = a.service.Read(k.LocalActor(), ref)
			if err != nil {
				return err
			}
		}
		if markdown {
			data, err := k.EncodeEntry(entry)
			if err != nil {
				return err
			}
			_, err = a.out.Write(data)
			return err
		}
		return a.print(entry, nil)
	}}
	read.Flags().StringVar(&readVersion, "version", "", "历史文件版本")
	read.Flags().BoolVar(&markdown, "markdown", false, "输出包含 front matter 的 Markdown")
	root.AddCommand(read)
	root.AddCommand(&cobra.Command{Use: "browse LIBRARY[/DOMAIN]", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.print(a.service.Browse(k.LocalActor(), scopes(args)[0]))
	}})
	root.AddCommand(&cobra.Command{Use: "history", RunE: func(cmd *cobra.Command, args []string) error {
		return a.print(a.service.History(k.LocalActor()))
	}})
	root.AddCommand(&cobra.Command{Use: "trace ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return a.print(a.service.Trace(k.LocalActor(), args[0]))
	}})
	root.AddCommand(&cobra.Command{Use: "reindex", RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.service.Reindex(cmd.Context(), k.LocalActor()); err != nil {
			return err
		}
		return a.print(map[string]string{"status": "ready"}, nil)
	}})
	root.AddCommand(&cobra.Command{Use: "export DIRECTORY", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.service.Export(k.LocalActor(), args[0]); err != nil {
			return err
		}
		return a.print(map[string]string{"status": "exported", "directory": args[0]}, nil)
	}})
}
