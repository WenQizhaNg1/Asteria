package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"asteria/internal/core"
	k "asteria/internal/knowledge"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

func scopes(values []string) []k.Scope {
	var result []k.Scope
	for _, value := range values {
		library, domain, _ := strings.Cut(value, "/")
		result = append(result, k.Scope{LibraryID: library, DomainID: domain})
	}
	return result
}

func budgetFlags(cmd *cobra.Command, budget *core.Budget) {
	cmd.Flags().IntVar(&budget.MaxSteps, "max-steps", 0, "最大模型调用次数")
	cmd.Flags().IntVar(&budget.MaxReads, "max-reads", 0, "最大文件读取次数")
	cmd.Flags().IntVar(&budget.MaxBytes, "max-bytes", 0, "最大正文读取字节数")
	cmd.Flags().IntVar(&budget.MaxProposals, "max-changes", 0, "最大提案文件变更数")
	cmd.Flags().IntVar(&budget.Seconds, "timeout", 0, "操作时限，单位秒")
}

func load(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) > 4<<20 {
		return fmt.Errorf("configuration exceeds 4 MiB")
	}
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("{")) || bytes.HasPrefix(trimmed, []byte("[")) {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err != nil {
			return err
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return fmt.Errorf("expected one JSON document")
		}
		return nil
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("expected one YAML document")
	}
	return nil
}

func readText(path string, input io.Reader) (string, error) {
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer file.Close()
		input = file
	}
	data, err := io.ReadAll(io.LimitReader(input, (2<<20)+1))
	if err != nil {
		return "", err
	}
	if len(data) > 2<<20 {
		return "", fmt.Errorf("input exceeds 2 MiB")
	}
	return string(data), nil
}
