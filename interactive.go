package main

// A simple program demonstrating the text area component from the Bubbles
// component library.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	llama "github.com/go-skynet/go-llama.cpp"
)

func startInteractive(l *llama.LLama, opts ...llama.PredictOption) error {
	p := tea.NewProgram(initialModel(l, opts...))

	_, err := p.Run()
	return err
}

type (
	errMsg error
)

type model struct {
	viewport    viewport.Model
	messages    *[]string
	textarea    textarea.Model
	senderStyle lipgloss.Style
	err         error
	l           *llama.LLama
	opts        []llama.PredictOption

	predictC chan string
}

func initialModel(l *llama.LLama, opts ...llama.PredictOption) model {
	ta := textarea.New()
	ta.Placeholder = "Send a message..."
	ta.Focus()

	ta.Prompt = "┃ "
	ta.CharLimit = 280

	ta.SetWidth(200)
	ta.SetHeight(3)

	// Remove cursor line styling
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()

	ta.ShowLineNumbers = false

	vp := viewport.New(200, 5)
	vp.SetContent(`Welcome to llama-cli. Type a message and press Enter to send. Alpaca doesn't keep context of the whole chat (yet).`)

	ta.KeyMap.InsertNewline.SetEnabled(false)

	predictChannel := make(chan string)
	messages := []string{}
	m := model{
		textarea:    ta,
		messages:    &messages,
		viewport:    vp,
		senderStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		err:         nil,
		l:           l,
		opts:        opts,
		predictC:    predictChannel,
	}
	go func() {
		for p := range predictChannel {
			str, _ := templateString(emptyInput, struct {
				Instruction string
				Input       string
			}{Instruction: p})
			res, _ := l.Predict(
				str,
				opts...,
			)

			mm := *m.messages
			*m.messages = mm[:len(mm)-1]
			*m.messages = append(*m.messages, m.senderStyle.Render("llama: ")+res)
			m.viewport.SetContent(strings.Join(*m.messages, "\n"))
			ta.Reset()
			m.viewport.GotoBottom()
		}
	}()

	return m
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	m.textarea, tiCmd = m.textarea.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:

	//	m.viewport.Width = msg.Width
	//	m.viewport.Height = msg.Height
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			fmt.Println(m.textarea.Value())
			return m, tea.Quit
		case tea.KeyEnter:
			*m.messages = append(*m.messages, m.senderStyle.Render("You: ")+m.textarea.Value(), m.senderStyle.Render("Loading response..."))
			m.predictC <- m.textarea.Value()
			m.viewport.SetContent(strings.Join(*m.messages, "\n"))
			m.textarea.Reset()
			m.viewport.GotoBottom()
		}

	// We handle errors just like any other message
	case errMsg:
		m.err = msg
		return m, nil
	}

	return m, tea.Batch(tiCmd, vpCmd)
}

func (m model) View() string {
	return fmt.Sprintf(
		"%s\n\n%s",
		m.viewport.View(),
		m.textarea.View(),
	) + "\n\n"
}
