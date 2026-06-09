package chat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

func runTerminalChat(ctx context.Context, session *chatSession, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	fmt.Fprintf(out, "LocalAI chat (%s)\n", session.CurrentModel())
	fmt.Fprintln(out, "Type /exit to quit, /clear to reset the conversation, /models to list models.")

	for {
		fmt.Fprint(out, "\n> ")
		if !scanner.Scan() {
			break
		}

		prompt := strings.TrimSpace(scanner.Text())
		switch prompt {
		case "":
			continue
		case "/bye", "/exit", "/quit":
			fmt.Fprintln(out, "bye")
			return nil
		case "/clear":
			session.Clear()
			fmt.Fprintln(out, "conversation cleared")
			continue
		case "/models":
			printChatModels(out, session.Models(), session.CurrentModel())
			continue
		}

		if nextModel, ok := strings.CutPrefix(prompt, "/model "); ok {
			nextModel = strings.TrimSpace(nextModel)
			if nextModel == "" {
				fmt.Fprintln(out, "usage: /model <name>")
				continue
			}
			if err := session.SwitchModel(nextModel); err != nil {
				fmt.Fprintln(out, err)
				continue
			}
			fmt.Fprintf(out, "switched to %s; conversation cleared\n", session.CurrentModel())
			continue
		}

		fmt.Fprint(out, "assistant: ")
		if err := session.Send(ctx, prompt, out); err != nil {
			return err
		}
		fmt.Fprintln(out)
	}

	return scanner.Err()
}

func printChatModels(out io.Writer, models []string, current string) {
	if len(models) == 0 {
		fmt.Fprintln(out, "no models installed")
		return
	}
	fmt.Fprint(out, formatChatModelList(models, current))
}
