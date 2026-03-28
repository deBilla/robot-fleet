package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var (
	apiURL string
	apiKey string
)

func main() {
	root := &cobra.Command{
		Use:   "fleetos",
		Short: "FleetOS CLI — Robot Fleet Management Platform",
		Long:  "CLI tool for managing robot fleets, sending commands, querying telemetry, and running AI inference.",
	}

	root.PersistentFlags().StringVar(&apiURL, "api-url", envOrDefault("FLEETOS_API_URL", "http://localhost:8080"), "FleetOS API base URL")
	root.PersistentFlags().StringVar(&apiKey, "api-key", envOrDefault("FLEETOS_API_KEY", "dev-key-001"), "API key for authentication")

	root.AddCommand(
		robotsCmd(),
		commandCmd(),
		telemetryCmd(),
		inferCmd(),
		metricsCmd(),
		usageCmd(),
		healthCmd(),
		semanticCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// --- Robots ---

func robotsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "robots",
		Short: "List and inspect robots in the fleet",
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List all robots",
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			resp, err := apiGet(fmt.Sprintf("/api/v1/robots?limit=%d", limit))
			if err != nil {
				return err
			}
			robots, _ := resp["robots"].([]any)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "ID\tSTATUS\tBATTERY\tPOSITION\n")
			for _, r := range robots {
				rm, _ := r.(map[string]any)
				fmt.Fprintf(w, "%s\t%s\t%.0f%%\t(%.1f, %.1f)\n",
					rm["ID"], rm["Status"],
					toFloat(rm["BatteryLevel"])*100,
					toFloat(rm["PosX"]), toFloat(rm["PosY"]),
				)
			}
			w.Flush()
			fmt.Printf("\nTotal: %v robots\n", resp["total"])
			return nil
		},
	}
	list.Flags().Int("limit", 20, "max robots to list")

	get := &cobra.Command{
		Use:   "get [robot-id]",
		Short: "Get robot details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := apiGet("/api/v1/robots/" + args[0])
			if err != nil {
				return err
			}
			prettyPrint(resp)
			return nil
		},
	}

	cmd.AddCommand(list, get)
	return cmd
}

// --- Command ---

func commandCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "command",
		Short: "Send commands to robots",
	}

	move := &cobra.Command{
		Use:   "move [robot-id] [x] [y]",
		Short: "Move robot to position",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := fmt.Sprintf(`{"type":"move","params":{"x":%s,"y":%s}}`, args[1], args[2])
			resp, err := apiPost("/api/v1/robots/"+args[0]+"/command", body)
			if err != nil {
				return err
			}
			fmt.Printf("Command queued: %v\n", resp["command_id"])
			return nil
		},
	}

	forward := &cobra.Command{
		Use:   "forward [robot-id] [distance]",
		Short: "Move robot forward (Menlo-compatible)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			dist := "1.0"
			if len(args) > 1 {
				dist = args[1]
			}
			body := fmt.Sprintf(`{"type":"move_relative","params":{"direction":"forward","distance":%s}}`, dist)
			resp, err := apiPost("/api/v1/robots/"+args[0]+"/command", body)
			if err != nil {
				return err
			}
			fmt.Printf("Command queued: %v\n", resp["command_id"])
			return nil
		},
	}

	stop := &cobra.Command{
		Use:   "stop [robot-id]",
		Short: "Stop robot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := apiPost("/api/v1/robots/"+args[0]+"/command", `{"type":"stop","params":{"emergency":false}}`)
			if err != nil {
				return err
			}
			fmt.Printf("Stop command queued: %v\n", resp["command_id"])
			return nil
		},
	}

	estop := &cobra.Command{
		Use:   "estop [robot-id]",
		Short: "Emergency stop robot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := apiPost("/api/v1/robots/"+args[0]+"/command", `{"type":"stop","params":{"emergency":true}}`)
			if err != nil {
				return err
			}
			fmt.Printf("E-Stop command queued: %v\n", resp["command_id"])
			return nil
		},
	}

	cmd.AddCommand(move, forward, stop, estop)
	return cmd
}

// --- Semantic Command (like Menlo's semantic-command) ---

func semanticCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "semantic [robot-id] [instruction...]",
		Short: "Send a natural language command to a robot (GR00T-style)",
		Long:  "Interprets natural language instructions and converts them to robot actions using AI inference. Mirrors Menlo's v1/robots/{id}/semantic-command API.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			robotID := args[0]
			instruction := strings.Join(args[1:], " ")
			body := fmt.Sprintf(`{"instruction":"%s","robot_id":"%s"}`, instruction, robotID)
			resp, err := apiPost("/api/v1/robots/"+robotID+"/semantic-command", body)
			if err != nil {
				return err
			}
			prettyPrint(resp)
			return nil
		},
	}
}

// --- Telemetry ---

func telemetryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "telemetry [robot-id]",
		Short: "Get latest telemetry for a robot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := apiGet("/api/v1/robots/" + args[0] + "/telemetry")
			if err != nil {
				return err
			}
			prettyPrint(resp)
			return nil
		},
	}
}

// --- Inference ---

func inferCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "infer [instruction]",
		Short: "Run GR00T-compatible AI inference",
		Long:  "Send a multimodal inference request (image + instruction) to predict robot joint actions. Compatible with NVIDIA GR00T N1 model format.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			instruction := strings.Join(args, " ")
			image, _ := cmd.Flags().GetString("image")
			modelID, _ := cmd.Flags().GetString("model")
			embodiment, _ := cmd.Flags().GetString("embodiment")
			body := fmt.Sprintf(`{"instruction":"%s","image":"%s","model_id":"%s","embodiment":"%s"}`,
				instruction, image, modelID, embodiment)
			resp, err := apiPost("/api/v1/inference", body)
			if err != nil {
				return err
			}
			prettyPrint(resp)
			return nil
		},
	}
	cmd.Flags().String("image", "", "base64-encoded image")
	cmd.Flags().String("model", "groot-n1-v1.5", "model ID")
	cmd.Flags().String("embodiment", "humanoid-v1", "robot embodiment tag (gr1, unitree-g1, etc.)")
	return cmd
}

// --- Metrics ---

func metricsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "metrics",
		Short: "Get fleet-wide metrics",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := apiGet("/api/v1/fleet/metrics")
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "Total Robots:\t%v\n", resp["total_robots"])
			fmt.Fprintf(w, "Active:\t%v\n", resp["active_robots"])
			fmt.Fprintf(w, "Idle:\t%v\n", resp["idle_robots"])
			fmt.Fprintf(w, "Errors:\t%v\n", resp["error_robots"])
			fmt.Fprintf(w, "Avg Battery:\t%.1f%%\n", toFloat(resp["avg_battery"])*100)
			w.Flush()
			return nil
		},
	}
}

// --- Usage ---

func usageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "usage",
		Short: "Get API usage for current tenant",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := apiGet("/api/v1/usage")
			if err != nil {
				return err
			}
			prettyPrint(resp)
			return nil
		},
	}
}

// --- Health ---

func healthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Check API health",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := apiGet("/healthz")
			if err != nil {
				return fmt.Errorf("API unreachable: %w", err)
			}
			fmt.Printf("API Status: %v\n", resp["status"])
			return nil
		},
	}
}

// --- HTTP helpers ---

func apiGet(path string) (map[string]any, error) {
	req, _ := http.NewRequest("GET", apiURL+path, nil)
	req.Header.Set("X-API-Key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode >= 400 {
		return result, fmt.Errorf("API error %d: %v", resp.StatusCode, result["error"])
	}
	return result, nil
}

func apiPost(path, body string) (map[string]any, error) {
	req, _ := http.NewRequest("POST", apiURL+path, strings.NewReader(body))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var result map[string]any
	json.Unmarshal(data, &result)
	if resp.StatusCode >= 400 {
		return result, fmt.Errorf("API error %d: %v", resp.StatusCode, result["error"])
	}
	return result, nil
}

func prettyPrint(v any) {
	data, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(data))
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	default:
		return 0
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
