//go:build !windows

package main

// A stub `coli` for the tests. The test binary runs itself as coli when
// COLIBRI_STUB_MODE is set, so no Python and no model are needed.
//
// `coli import <gguf> --out <dir>` writes <dir>/config.json.
// `coli serve ... --port P` serves on 127.0.0.1:P, checking COLI_API_KEY:
//
//	ok         answers chat and completions requests (see below)
//	startfail  prints a reason on stderr and exits with status 3
//	slow       streams one token every 50 ms until the client goes away,
//	           then appends "disconnected" to COLIBRI_STUB_LOG
//	crash      like ok, but exits in the middle of the first chat request it
//	           gets in the life of COLIBRI_STUB_MARK (a file it creates);
//	           with COLIBRI_STUB_NORESTART set, a start that finds the file
//	           fails instead
//
// Every request body is appended to COLIBRI_STUB_LOG, one per line.
// A chat request with tools gets a tool call back; one with
// reasoning_effort gets reasoning too.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("COLIBRI_STUB_MODE"); mode != "" {
		runStub(mode, os.Args[1:])
		return
	}
	os.Exit(m.Run())
}

func stubArg(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func stubLog(s string) {
	p := os.Getenv("COLIBRI_STUB_LOG")
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(f, s)
	_ = f.Close()
}

func runStub(mode string, args []string) {
	if len(args) > 0 && args[0] == "import" {
		out := stubArg(args, "--out")
		_ = os.MkdirAll(out, 0o750)
		_ = os.WriteFile(filepath.Join(out, "config.json"), []byte(`{"model_type":"qwen3"}`), 0o600)
		stubLog("import " + args[1])
		os.Exit(0)
	}
	mark := os.Getenv("COLIBRI_STUB_MARK")
	if mode == "startfail" {
		fmt.Fprintln(os.Stderr, "loading the model...")
		fmt.Fprintln(os.Stderr, "error: this model needs 99 GB of RAM, 8 GB available")
		os.Exit(3)
	}
	if mode == "crash" && os.Getenv("COLIBRI_STUB_NORESTART") != "" && mark != "" {
		if _, err := os.Stat(mark); err == nil {
			fmt.Fprintln(os.Stderr, "error: the engine binary is gone")
			os.Exit(4)
		}
	}
	key := os.Getenv("COLI_API_KEY")
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, _ = io.WriteString(w, `{"status":"ok","arch":"stub"}`)
	})
	handle := func(chat bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !auth(w, r) {
				return
			}
			body, _ := io.ReadAll(r.Body)
			stubLog(string(body))
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			if _, ok := req["logit_bias"]; ok {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"Colibri does not support a non-empty `+"`logit_bias`"+` yet.","param":"logit_bias","code":"unsupported_value"}}`)
				return
			}
			stream, _ := req["stream"].(bool)
			_, tools := req["tools"]
			_, reasoning := req["reasoning_effort"]
			if mode == "slow" {
				serveSlow(w, r)
				return
			}
			if mode == "crash" && chat && mark != "" {
				if _, err := os.Stat(mark); err != nil {
					_ = os.WriteFile(mark, []byte("1"), 0o600)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"Hel"}}]}`+"\n\n")
					w.(http.Flusher).Flush()
					fmt.Fprintln(os.Stderr, "Fatal Python error: Segmentation fault")
					os.Exit(9)
				}
			}
			if stream {
				serveStream(w, chat, tools, reasoning)
			} else {
				serveJSON(w, chat, tools, reasoning)
			}
		}
	}
	mux.HandleFunc("/v1/chat/completions", handle(true))
	mux.HandleFunc("/v1/completions", handle(false))
	fmt.Println("OpenAI-compatible API listening")
	srv := &http.Server{Addr: "127.0.0.1:" + stubArg(args, "--port"), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serveJSON(w http.ResponseWriter, chat, tools, reasoning bool) {
	w.Header().Set("Content-Type", "application/json")
	usage := `"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}`
	if !chat {
		_, _ = io.WriteString(w, `{"choices":[{"index":0,"text":" Paris.","logprobs":null,"finish_reason":"stop"}],`+usage+`}`)
		return
	}
	msg := `{"role":"assistant","content":"Hello there."`
	if reasoning {
		msg += `,"reasoning_content":"The user greets me."`
	}
	if tools {
		msg = `{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]`
	}
	_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":`+msg+`},"finish_reason":"stop"}],`+usage+`}`)
}

func serveStream(w http.ResponseWriter, chat, tools, reasoning bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(s string) {
		_, _ = io.WriteString(w, "data: "+s+"\n\n")
		w.(http.Flusher).Flush()
	}
	_, _ = io.WriteString(w, ": keepalive\n\n")
	if !chat {
		for _, t := range []string{" Pa", "ris."} {
			send(`{"choices":[{"index":0,"text":"` + t + `"}]}`)
		}
	} else {
		send(`{"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`)
		if reasoning {
			send(`{"choices":[{"index":0,"delta":{"reasoning_content":"Thinking."}}]}`)
		}
		if tools {
			send(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]}}]}`)
			send(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}`)
			send(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Paris\"}"}}]}}]}`)
		} else {
			for _, t := range []string{"Hello", " there."} {
				send(`{"choices":[{"index":0,"delta":{"content":"` + t + `"}}]}`)
			}
		}
		send(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	}
	send(`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`)
	send("[DONE]")
}

func serveSlow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	for i := 0; ; i++ {
		select {
		case <-r.Context().Done():
			stubLog("disconnected")
			return
		case <-time.After(50 * time.Millisecond):
		}
		if _, err := io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"tok `+strings.Repeat("x", i%3)+`"}}]}`+"\n\n"); err != nil {
			stubLog("disconnected")
			return
		}
		w.(http.Flusher).Flush()
	}
}
