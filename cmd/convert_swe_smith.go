package cmd

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/inference-sim/inference-sim/sim/workload"
)

var (
	sweSmithInputPath  string
	sweSmithOutputPath string
	sweSmithLimit      int
	sweSmithTurnGapMs  int64
)

var convertSWESmithCmd = &cobra.Command{
	Use:   "swe-smith",
	Short: "Convert SWE-Smith workload JSON to TraceV2",
	Long: `Convert a SWE-Smith workload JSON file (swe_smith_workload.json) to BLIS TraceV2
format (.yaml header + .csv data) suitable for blis replay.

Each trajectory becomes a closed-loop session. Turn k uses all prior messages as
prefix and the current user message as input (suffix). Token counts are estimated
from character length (chars/4).

Arrival times are synthesized: session starts at t=0, turn k arrives at
k × turn-gap-ms milliseconds to give the simulator time to process each turn.

Example:
  blis convert swe-smith \
    --input swe_smith_workload.json \
    --output traces/swe_smith \
    --limit 50 \
    --turn-gap-ms 30000`,
	Run: func(cmd *cobra.Command, args []string) {
		if sweSmithTurnGapMs <= 0 {
			logrus.Fatalf("--turn-gap-ms must be positive, got %d", sweSmithTurnGapMs)
		}
		turnGapUs := sweSmithTurnGapMs * 1000

		header, records, err := workload.ConvertSWESmith(sweSmithInputPath, sweSmithLimit, turnGapUs)
		if err != nil {
			logrus.Fatalf("SWE-Smith conversion failed: %v", err)
		}

		headerPath := sweSmithOutputPath + ".yaml"
		dataPath := sweSmithOutputPath + ".csv"

		if err := workload.ExportTraceV2(header, records, headerPath, dataPath); err != nil {
			logrus.Fatalf("Writing TraceV2 output failed: %v", err)
		}

		logrus.Infof("Converted %d records → %s + %s", len(records), headerPath, dataPath)
	},
}

func init() {
	convertSWESmithCmd.Flags().StringVar(&sweSmithInputPath, "input", "", "Path to SWE-Smith workload JSON file")
	convertSWESmithCmd.Flags().StringVar(&sweSmithOutputPath, "output", "", "Output path prefix (writes PREFIX.yaml and PREFIX.csv)")
	convertSWESmithCmd.Flags().IntVar(&sweSmithLimit, "limit", 0, "Maximum number of trajectories to convert (0 = no limit)")
	convertSWESmithCmd.Flags().Int64Var(&sweSmithTurnGapMs, "turn-gap-ms", 30000, "Simulated gap between turns in milliseconds")
	_ = convertSWESmithCmd.MarkFlagRequired("input")
	_ = convertSWESmithCmd.MarkFlagRequired("output")

	convertCmd.AddCommand(convertSWESmithCmd)
}
