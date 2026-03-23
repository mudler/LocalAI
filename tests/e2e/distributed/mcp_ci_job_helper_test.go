package distributed_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/cogito"
	"github.com/mudler/cogito/clients"
	"github.com/mudler/xlog"
)

// processMCPCIJobForTest replicates the logic of handleMCPCIJob from agent_worker.go
// for testing purposes. This allows e2e testing of the full MCP CI job execution path
// without needing to start an actual agent worker binary.
func processMCPCIJobForTest(data []byte, apiURL, apiToken string, natsClient *messaging.Client) {
	var evt jobs.JobEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		xlog.Error("Failed to unmarshal job event", "error", err)
		return
	}

	job := evt.Job
	task := evt.Task
	if job == nil || task == nil {
		xlog.Error("MCP CI job missing enriched data", "jobID", evt.JobID)
		publishTestJobResult(natsClient, evt.JobID, "failed", "", "job or task data missing from NATS event")
		return
	}

	modelCfg := evt.ModelConfig
	if modelCfg == nil {
		publishTestJobResult(natsClient, evt.JobID, "failed", "", "model config missing from job event")
		return
	}

	// Publish running status
	natsClient.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
		JobID: evt.JobID, Status: "running", Message: "Job started on test worker",
	})

	// Parse MCP config
	if modelCfg.MCP.Servers == "" && modelCfg.MCP.Stdio == "" {
		publishTestJobResult(natsClient, evt.JobID, "failed", "", "no MCP servers configured for model")
		return
	}

	remote, stdio, err := modelCfg.MCP.MCPConfigFromYAML()
	if err != nil {
		publishTestJobResult(natsClient, evt.JobID, "failed", "", fmt.Sprintf("failed to parse MCP config: %v", err))
		return
	}

	// Create MCP sessions
	sessions, err := mcpTools.SessionsFromMCPConfig(modelCfg.Name, remote, stdio)
	if err != nil || len(sessions) == 0 {
		errMsg := "no working MCP servers found"
		if err != nil {
			errMsg = fmt.Sprintf("failed to create MCP sessions: %v", err)
		}
		publishTestJobResult(natsClient, evt.JobID, "failed", "", errMsg)
		return
	}

	// Build prompt from template
	prompt := task.Prompt
	if task.CronParametersJSON != "" {
		var params map[string]string
		json.Unmarshal([]byte(task.CronParametersJSON), &params)
		for k, v := range params {
			prompt = strings.ReplaceAll(prompt, "{{."+k+"}}", v)
		}
	}
	if job.ParametersJSON != "" {
		var params map[string]string
		json.Unmarshal([]byte(job.ParametersJSON), &params)
		for k, v := range params {
			prompt = strings.ReplaceAll(prompt, "{{."+k+"}}", v)
		}
	}

	// Create LLM client pointing to mock API
	llm := clients.NewLocalAILLM(task.Model, apiToken, apiURL+"/v1")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Publish running status
	publishTestJobStatus(natsClient, evt.JobID, "running", "")

	// Buffer stream tokens
	var reasoningBuf, contentBuf strings.Builder
	var lastStreamType cogito.StreamEventType

	flushStreamBuf := func() {
		if reasoningBuf.Len() > 0 {
			natsClient.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "reasoning", TraceContent: reasoningBuf.String(),
			})
			reasoningBuf.Reset()
		}
		if contentBuf.Len() > 0 {
			natsClient.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "content", TraceContent: contentBuf.String(),
			})
			contentBuf.Reset()
		}
	}

	cogitoOpts := buildTestCogitoOptions(modelCfg)
	cogitoOpts = append(cogitoOpts,
		cogito.WithContext(ctx),
		cogito.WithMCPs(sessions...),
		cogito.WithStatusCallback(func(status string) {
			flushStreamBuf()
			natsClient.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "status", TraceContent: status,
			})
		}),
		cogito.WithToolCallResultCallback(func(t cogito.ToolStatus) {
			flushStreamBuf()
			natsClient.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "tool_result", TraceContent: fmt.Sprintf("%s: %s", t.Name, t.Result),
			})
		}),
		cogito.WithStreamCallback(func(ev cogito.StreamEvent) {
			if ev.Type != lastStreamType {
				flushStreamBuf()
				lastStreamType = ev.Type
			}
			switch ev.Type {
			case cogito.StreamEventReasoning:
				reasoningBuf.WriteString(ev.Content)
			case cogito.StreamEventContent:
				contentBuf.WriteString(ev.Content)
			case cogito.StreamEventToolCall:
				natsClient.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
					JobID: evt.JobID, TraceType: "tool_call", TraceContent: fmt.Sprintf("%s(%s)", ev.ToolName, ev.ToolArgs),
				})
			}
		}),
	)

	// Execute via cogito
	fragment := cogito.NewEmptyFragment()
	fragment = fragment.AddMessage("user", prompt)

	f, err := cogito.ExecuteTools(llm, fragment, cogitoOpts...)
	flushStreamBuf()

	if err != nil {
		publishTestJobResult(natsClient, evt.JobID, "failed", "", fmt.Sprintf("cogito execution failed: %v", err))
		return
	}

	result := ""
	if msg := f.LastMessage(); msg != nil {
		result = msg.Content
	}
	publishTestJobResult(natsClient, evt.JobID, "completed", result, "")
}

func publishTestJobStatus(nc *messaging.Client, jobID, status, message string) {
	nc.Publish(messaging.SubjectJobResult(jobID), jobs.JobResultEvent{
		JobID:  jobID,
		Status: status,
	})
	nc.Publish(messaging.SubjectJobProgress(jobID), jobs.ProgressEvent{
		JobID: jobID, Status: status, Message: message,
	})
}

func publishTestJobResult(nc *messaging.Client, jobID, status, result, errMsg string) {
	nc.Publish(messaging.SubjectJobResult(jobID), jobs.JobResultEvent{
		JobID:  jobID,
		Status: status,
		Result: result,
		Error:  errMsg,
	})
	nc.Publish(messaging.SubjectJobProgress(jobID), jobs.ProgressEvent{
		JobID: jobID, Status: status, Message: errMsg,
	})
}

// buildTestCogitoOptions mirrors ModelConfig.BuildCogitoOptions for tests.
func buildTestCogitoOptions(cfg *config.ModelConfig) []cogito.Option {
	opts := []cogito.Option{
		cogito.WithIterations(3),
		cogito.WithMaxAttempts(3),
	}

	if cfg.Agent.MaxIterations > 0 {
		opts = append(opts, cogito.WithIterations(cfg.Agent.MaxIterations))
	}
	if cfg.Agent.MaxAttempts > 0 {
		opts = append(opts, cogito.WithMaxAttempts(cfg.Agent.MaxAttempts))
	}
	if cfg.Agent.LoopDetection > 0 {
		opts = append(opts, cogito.WithLoopDetection(cfg.Agent.LoopDetection))
	}
	if cfg.Agent.DisableSinkState {
		opts = append(opts, cogito.DisableSinkState)
	}

	return opts
}
