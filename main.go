package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"runtime"
	"text/template"

	llama "github.com/go-skynet/llama/go"
	"github.com/urfave/cli/v2"
)

// Define the template string
var emptyInput string = `Below is an instruction that describes a task. Write a response that appropriately completes the request.

### Instruction:
{{.Instruction}}

### Response:`

var nonEmptyInput string = `Below is an instruction that describes a task, paired with an input that provides further context. Write a response that appropriately completes the request.

### Instruction:
{{.Instruction}}

### Input:
{{.Input}}

### Response:
`

func llamaFromOptions(ctx *cli.Context) (*llama.LLama, error) {
	opts := []llama.ModelOption{llama.SetContext(ctx.Int("context-size"))}
	if ctx.Bool("alpaca") {
		opts = append(opts, llama.EnableAlpaca)
	}
	if ctx.Bool("gpt4all") {
		opts = append(opts, llama.EnableGPT4All)
	}
	return llama.New(ctx.String("model"), opts...)
}

func templateString(t string, in interface{}) (string, error) {
	// Parse the template
	tmpl, err := template.New("prompt").Parse(t)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, in)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

var modelFlags = []cli.Flag{
	&cli.StringFlag{
		Name:    "model",
		EnvVars: []string{"MODEL_PATH"},
	},
	&cli.IntFlag{
		Name:    "tokens",
		EnvVars: []string{"TOKENS"},
		Value:   128,
	},
	&cli.IntFlag{
		Name:    "context-size",
		EnvVars: []string{"CONTEXT_SIZE"},
		Value:   512,
	},
	&cli.IntFlag{
		Name:    "threads",
		EnvVars: []string{"THREADS"},
		Value:   runtime.NumCPU(),
	},
	&cli.Float64Flag{
		Name:    "temperature",
		EnvVars: []string{"TEMPERATURE"},
		Value:   0.95,
	},
	&cli.Float64Flag{
		Name:    "topp",
		EnvVars: []string{"TOP_P"},
		Value:   0.85,
	},
	&cli.IntFlag{
		Name:    "topk",
		EnvVars: []string{"TOP_K"},
		Value:   20,
	},
	&cli.BoolFlag{
		Name:    "alpaca",
		EnvVars: []string{"ALPACA"},
		Value:   true,
	},
	&cli.BoolFlag{
		Name:    "gpt4all",
		EnvVars: []string{"GPT4ALL"},
		Value:   false,
	},
}

func main() {
	app := &cli.App{
		Name:    "llama-cli",
		Version: "0.1",
		Usage:   "llama-cli --model ... --instruction 'What is an alpaca?'",
		Flags: append(modelFlags,
			&cli.StringFlag{
				Name:    "template",
				EnvVars: []string{"TEMPLATE"},
			},
			&cli.StringFlag{
				Name:    "instruction",
				EnvVars: []string{"INSTRUCTION"},
			},
			&cli.StringFlag{
				Name:    "input",
				EnvVars: []string{"INPUT"},
			}),
		Description: `Run llama.cpp inference`,
		UsageText: `
llama-cli --model ~/ggml-alpaca-7b-q4.bin --instruction "What's an alpaca?"

	An Alpaca (Vicugna pacos) is a domesticated species of South American camelid, related to llamas and originally from Peru but now found throughout much of Andean region. They are bred for their fleeces which can be spun into wool or knitted items such as hats, sweaters, blankets etc
		
echo "An Alpaca (Vicugna pacos) is a domesticated species of South American camelid, related to llamas and originally from Peru but now found throughout much of Andean region. They are bred for their fleeces which can be spun into wool or knitted items such as hats, sweaters, blankets etc" | llama-cli --model ~/ggml-alpaca-7b-q4.bin --instruction "Proofread, improving clarity and flow" --input "-"

	An Alpaca (Vicugna pacos) is a domesticated species from South America that's related to llamas. Originating in Peru but now found throughout the Andean region, they are bred for their fleeces which can be spun into wool or knitted items such as hats and sweaters—blankets too!
`,
		Copyright: "go-skynet authors",
		Commands: []*cli.Command{
			{
				Flags: modelFlags,
				Name:  "interactive",
				Action: func(ctx *cli.Context) error {

					l, err := llamaFromOptions(ctx)
					if err != nil {
						fmt.Println("Loading the model failed:", err.Error())
						os.Exit(1)
					}

					return startInteractive(l, llama.SetTemperature(ctx.Float64("temperature")),
						llama.SetTopP(ctx.Float64("topp")),
						llama.SetTopK(ctx.Int("topk")),
						llama.SetTokens(ctx.Int("tokens")),
						llama.SetThreads(ctx.Int("threads")))
				},
			},
			{

				Name: "api",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:    "threads",
						EnvVars: []string{"THREADS"},
						Value:   runtime.NumCPU(),
					},
					&cli.StringFlag{
						Name:    "model",
						EnvVars: []string{"MODEL_PATH"},
					},
					&cli.StringFlag{
						Name:    "address",
						EnvVars: []string{"ADDRESS"},
						Value:   ":8080",
					},
					&cli.BoolFlag{
						Name:    "alpaca",
						EnvVars: []string{"ALPACA"},
						Value:   true,
					},
					&cli.BoolFlag{
						Name:    "gpt4all",
						EnvVars: []string{"GPT4ALL"},
						Value:   false,
					},
					&cli.IntFlag{
						Name:    "context-size",
						EnvVars: []string{"CONTEXT_SIZE"},
						Value:   512,
					},
				},
				Action: func(ctx *cli.Context) error {
					l, err := llamaFromOptions(ctx)
					if err != nil {
						fmt.Println("Loading the model failed:", err.Error())
						os.Exit(1)
					}

					return api(l, ctx.String("address"), ctx.Int("threads"))
				},
			},
		},
		Action: func(ctx *cli.Context) error {

			instruction := ctx.String("instruction")
			input := ctx.String("input")
			templ := ctx.String("template")

			promptTemplate := ""

			if input != "" {
				promptTemplate = nonEmptyInput
			} else {
				promptTemplate = emptyInput
			}

			if templ != "" {
				dat, err := os.ReadFile(templ)
				if err != nil {
					fmt.Printf("Failed reading file: %s", err.Error())
					os.Exit(1)
				}
				promptTemplate = string(dat)
			}

			if instruction == "-" {
				dat, err := ioutil.ReadAll(os.Stdin)
				if err != nil {
					fmt.Printf("reading stdin failed: %s", err)
					os.Exit(1)
				}
				instruction = string(dat)
			}

			if input == "-" {
				dat, err := ioutil.ReadAll(os.Stdin)
				if err != nil {
					fmt.Printf("reading stdin failed: %s", err)
					os.Exit(1)
				}
				input = string(dat)
			}

			str, err := templateString(promptTemplate, struct {
				Instruction string
				Input       string
			}{Instruction: instruction, Input: input})

			if err != nil {
				fmt.Println("Templating the input failed:", err.Error())
				os.Exit(1)
			}

			l, err := llamaFromOptions(ctx)
			if err != nil {
				fmt.Println("Loading the model failed:", err.Error())
				os.Exit(1)
			}

			res, err := l.Predict(
				str,
				llama.SetTemperature(ctx.Float64("temperature")),
				llama.SetTopP(ctx.Float64("topp")),
				llama.SetTopK(ctx.Int("topk")),
				llama.SetTokens(ctx.Int("tokens")),
				llama.SetThreads(ctx.Int("threads")),
			)
			if err != nil {
				fmt.Printf("predicting failed: %s", err)
				os.Exit(1)
			}
			fmt.Println(res)
			return nil
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
