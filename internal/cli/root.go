// Package cli provides the local human interface to the shared core service.
package cli

import (
	"encoding/json"
	"io"
	"os"

	"asteria/internal/core"
	"asteria/internal/store"
	"github.com/spf13/cobra"
)

type application struct {
	service *core.Service
	in      io.Reader
	out     io.Writer
	errOut  io.Writer
}

func New(in io.Reader, out, errOut io.Writer) *cobra.Command {
	a := &application{in: in, out: out, errOut: errOut}
	var dataDir string
	var offline bool
	root := &cobra.Command{
		Use: "asteria", Short: "人和 Agent 共同维护的 Markdown 知识引擎",
		SilenceUsage: true, SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			storage, err := store.Open(dataDir)
			if err != nil {
				return err
			}
			var model core.Model
			if !offline {
				model, err = modelFromEnv()
				if err != nil {
					return err
				}
			}
			a.service = core.New(storage, model)
			return nil
		},
	}
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(errOut)
	defaultData := os.Getenv("ASTERIA_DATA")
	if defaultData == "" {
		defaultData = "./data"
	}
	root.PersistentFlags().StringVar(&dataDir, "data", defaultData, "数据目录")
	root.PersistentFlags().BoolVar(&offline, "offline", false, "禁用模型，仅进行确定性操作与检索")
	addKnowledgeCommands(root, a)
	addManagementCommands(root, a)
	addServerCommands(root, a)
	return root
}

func (a *application) print(value any, err error) error {
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(a.out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
