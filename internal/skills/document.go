package skills

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hitk/hitk/internal/document"
	"github.com/hitk/hitk/internal/protocol"
	"github.com/spf13/cobra"
)

type DocumentContext struct {
	*Context
	Documents *document.Engine
	Workspace string
}

func RunDocument(ctx *DocumentContext, command string, args []string) (Result, error) {
	if ctx == nil || ctx.Documents == nil {
		return Result{}, errors.New("文档引擎不可用")
	}
	root := &cobra.Command{Use: "document", SilenceUsage: true, SilenceErrors: true, DisableFlagParsing: false}
	root.SetHelpCommand(&cobra.Command{Hidden: true})
	root.SetOut(&commandOutputWriter{output: &commandOutput{}})
	root.SetErr(root.OutOrStderr())
	root.AddCommand(documentCommand(ctx, "inspect"), documentCommand(ctx, "extract"), documentCommand(ctx, "open"), documentCommand(ctx, "convert"))
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return Result{}, errors.New("skill_run 不支持帮助参数")
		}
	}
	root.SetArgs(append([]string{strings.ToLower(strings.TrimSpace(command))}, args...))
	output := root.OutOrStdout().(*commandOutputWriter).output
	if err := root.ExecuteContext(ctx.Ctx); err != nil {
		return Result{}, err
	}
	if output.result == nil {
		return Result{}, errors.New("document command 未返回结果")
	}
	return *output.result, nil
}

func documentCommand(ctx *DocumentContext, operation string) *cobra.Command {
	var path string
	var offset, limit int
	cmd := &cobra.Command{Use: operation, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if strings.TrimSpace(path) == "" {
			return errors.New(operation + " 需要 --path")
		}
		var result document.Result
		var err error
		switch operation {
		case "inspect":
			result, err = ctx.Documents.Inspect(ctx.Ctx, path, ctx.Workspace)
		case "extract":
			result, err = ctx.Documents.Extract(ctx.Ctx, path, ctx.Workspace)
		case "open":
			openArgs := []string{"--offset", fmt.Sprint(max(offset, 0))}
			if limit > 0 {
				openArgs = append(openArgs, "--limit", fmt.Sprint(limit))
			}
			result, err = ctx.Documents.Open(ctx.Ctx, path, ctx.Workspace, openArgs)
		case "convert":
			result, err = ctx.Documents.Convert(ctx.Ctx, path, ctx.Workspace)
		}
		if err != nil {
			return err
		}
		artifacts := make([]protocol.Artifact, 0, len(result.Artifacts))
		for _, artifact := range result.Artifacts {
			artifacts = append(artifacts, protocol.Artifact{Path: artifact.Path, Name: artifact.Name, Mime: artifact.Mime, Size: artifact.Size, Operation: artifact.Operation})
		}
		content := result.Text
		if result.SkippedReason != "" {
			content = result.SkippedReason
		}
		return writeResult(cmd, Result{Content: content, Display: fmt.Sprintf("%s %s", operation, result.Title), Artifacts: artifacts, Truncated: result.Truncated, Meta: map[string]any{"format": result.Format}})
	}}
	cmd.Flags().StringVar(&path, "path", "", "文档绝对路径")
	if operation == "open" {
		cmd.Flags().IntVar(&offset, "offset", 0, "文本起始偏移")
		cmd.Flags().IntVar(&limit, "limit", 96<<10, "最大返回字符数")
	}
	return cmd
}
