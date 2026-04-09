package cmd

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/inference-sim/inference-sim/sim/workload"
)

var (
	chatTraceInputPath  string
	chatTraceOutputPath string
	chatTraceLimit      int
)

var convertChatTraceCmd = &cobra.Command{
	Use:   "chat-trace",
	Short: "Convert multi-turn chat production trace JSONL to TraceV2",
	Long: `Convert a multi-turn chat production trace JSONL file to BLIS TraceV2
format (.yaml header + .csv data) suitable for blis replay.

Session structure and prefix lengths are derived from the trace's parent_chat_id
chain and hash_id block overlap between consecutive turns.

Example:
  blis convert chat-trace \
    --input trace_blksz_16.jsonl \
    --output traces/chat \
    --limit 5000`,
	Run: func(cmd *cobra.Command, args []string) {
		header, records, err := workload.ConvertChatTrace(chatTraceInputPath, chatTraceLimit)
		if err != nil {
			logrus.Fatalf("Chat trace conversion failed: %v", err)
		}

		headerPath := chatTraceOutputPath + ".yaml"
		dataPath := chatTraceOutputPath + ".csv"

		if err := workload.ExportTraceV2(header, records, headerPath, dataPath); err != nil {
			logrus.Fatalf("Writing TraceV2 output failed: %v", err)
		}

		logrus.Infof("Converted %d records → %s + %s", len(records), headerPath, dataPath)
	},
}

func init() {
	convertChatTraceCmd.Flags().StringVar(&chatTraceInputPath, "input", "", "Path to chat trace JSONL file")
	convertChatTraceCmd.Flags().StringVar(&chatTraceOutputPath, "output", "", "Output path prefix (writes PREFIX.yaml and PREFIX.csv)")
	convertChatTraceCmd.Flags().IntVar(&chatTraceLimit, "limit", 5000, "Maximum number of records to include (0 = no limit)")
	_ = convertChatTraceCmd.MarkFlagRequired("input")
	_ = convertChatTraceCmd.MarkFlagRequired("output")

	convertCmd.AddCommand(convertChatTraceCmd)
}
