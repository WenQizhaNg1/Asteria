package cli

import (
	"strings"

	"asteria/internal/core"
	k "asteria/internal/knowledge"
	"github.com/spf13/cobra"
)

func addKnowledgeCommands(root *cobra.Command, a *application) {
	var writeRequest core.WriteRequest
	var writeFile string
	var writeScopes []string
	write := &cobra.Command{Use: "write", Short: "录入有来源的 Markdown", RunE: func(cmd *cobra.Command, args []string) error {
		content, err := readText(writeFile, a.in)
		if err != nil {
			return err
		}
		writeRequest.Content, writeRequest.Scope = content, scopes(writeScopes)
		return a.print(a.service.Write(cmd.Context(), k.LocalActor(), writeRequest))
	}}
	write.Flags().StringVar(&writeFile, "file", "-", "Markdown 文件，- 表示标准输入")
	write.Flags().StringVar(&writeRequest.Source, "source", "", "原始来源标识或 URL")
	write.Flags().StringVar(&writeRequest.LibraryID, "library", "", "知识库 ID")
	write.Flags().StringVar(&writeRequest.DomainID, "domain", "", "领域 ID")
	write.Flags().StringVar(&writeRequest.Title, "title", "", "条目标题")
	write.Flags().StringSliceVar(&writeScopes, "scope", nil, "范围：library 或 library/domain")
	_ = write.MarkFlagRequired("source")
	budgetFlags(write, &writeRequest.Budget)
	root.AddCommand(write)

	var queryRequest core.QueryRequest
	var queryScopes []string
	query := &cobra.Command{Use: "query QUESTION", Short: "查询知识与版本化证据", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		queryRequest.Question, queryRequest.Scope = strings.Join(args, " "), scopes(queryScopes)
		return a.print(a.service.Query(cmd.Context(), k.LocalActor(), queryRequest))
	}}
	query.Flags().StringSliceVar(&queryScopes, "scope", nil, "范围：library 或 library/domain")
	budgetFlags(query, &queryRequest.Budget)
	root.AddCommand(query)

	var reviseRequest core.ReviseRequest
	var reviseFile, reviseSchema, evidenceFile, reviewID, reviewStatus, reviewNote string
	revise := &cobra.Command{Use: "revise TARGET", Short: "生成正文、领域或关联修订提案", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		reviseRequest.Target = args[0]
		if cmd.Flags().Changed("file") {
			content, err := readText(reviseFile, a.in)
			if err != nil {
				return err
			}
			reviseRequest.Content = &content
		}
		if reviseSchema != "" {
			var schema k.Schema
			if err := load(reviseSchema, &schema); err != nil {
				return err
			}
			reviseRequest.Schema = &schema
		}
		if evidenceFile != "" {
			if err := load(evidenceFile, &reviseRequest.Evidence); err != nil {
				return err
			}
		}
		if reviewID != "" {
			reviseRequest.Review = &core.RelationReview{ID: reviewID, Status: reviewStatus, Note: reviewNote, Evidence: reviseRequest.Evidence}
		}
		return a.print(a.service.Revise(cmd.Context(), k.LocalActor(), reviseRequest))
	}}
	revise.Flags().StringVar(&reviseRequest.Instruction, "instruction", "", "修订意图")
	revise.Flags().StringVar(&reviseFile, "file", "", "替换正文文件")
	revise.Flags().StringVar(&reviseRequest.Title, "title", "", "新标题")
	revise.Flags().StringVar(&reviseRequest.Home, "home", "", "新的主要领域")
	revise.Flags().StringVar(&reviseRequest.Source, "source", "", "补充来源标识")
	revise.Flags().BoolVar(&reviseRequest.Withdraw, "withdraw", false, "撤回条目，保留历史")
	revise.Flags().StringVar(&reviseRequest.RestoreVersion, "restore", "", "以历史版本形成新修订")
	revise.Flags().StringVar(&reviseSchema, "schema", "", "完整新领域配置")
	revise.Flags().StringVar(&reviseRequest.Base, "base", "", "领域配置的基准 commit_id")
	revise.Flags().StringVar(&evidenceFile, "evidence", "", "版本化证据引用的 JSON 数组")
	revise.Flags().StringVar(&reviewID, "review-relation", "", "核查关联 ID")
	revise.Flags().StringVar(&reviewStatus, "status", "verified", "verified 或 rejected")
	revise.Flags().StringVar(&reviewNote, "note", "", "人工核查说明")
	_ = revise.MarkFlagRequired("instruction")
	budgetFlags(revise, &reviseRequest.Budget)
	root.AddCommand(revise)

	var reflectRequest core.ReflectRequest
	var reflectScopes []string
	reflect := &cobra.Command{Use: "reflect", Short: "发现候选联系并提出维护建议", RunE: func(cmd *cobra.Command, args []string) error {
		reflectRequest.Scope = scopes(reflectScopes)
		return a.print(a.service.Reflect(cmd.Context(), k.LocalActor(), reflectRequest))
	}}
	reflect.Flags().StringVar(&reflectRequest.Goal, "goal", "", "内省目标")
	reflect.Flags().StringSliceVar(&reflectScopes, "scope", nil, "范围：library 或 library/domain")
	budgetFlags(reflect, &reflectRequest.Budget)
	root.AddCommand(reflect)
}
