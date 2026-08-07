package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func main() {
	root := &cobra.Command{
		Use:   "app",
		Short: "Resume search platform — serve the API or run an ingestion worker",
	}
	root.AddCommand(serveCmd())
	root.AddCommand(workerCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func requireEnv(name string) string {
	v := os.Getenv(name)
	if v == "" {
		fmt.Fprintf(os.Stderr, "missing required env var %s\n", name)
		os.Exit(1)
	}
	return v
}

// splitBrokers parses a comma-separated KAFKA_BROKERS value ("kafka1:9092,kafka2:9092")
// into the slice the Kafka client constructors expect.
func splitBrokers(v string) []string {
	parts := strings.Split(v, ",")
	brokers := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			brokers = append(brokers, p)
		}
	}
	return brokers
}
