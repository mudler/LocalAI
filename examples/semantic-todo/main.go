package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	localAI     string = "http://localhost:8080"
	rootStatus  string = "[::b]<space>[::-]: Add Task  [::b]/[::-]: Search Task  [::b]<C-c>[::-]: Exit"
	inputStatus string = "Press [::b]<enter>[::-] to submit the task, [::b]<esc>[::-] to cancel"
)

type Task struct {
	Description string
	Similarity  float32
}

type AppState int

const (
	StateRoot AppState = iota
	StateInput
	StateSearch
)

type App struct {
	state AppState
	tasks []Task
	app   *tview.Application
	flex  *tview.Flex
	table *tview.Table
}

func NewApp() *App {
	return &App{
		state: StateRoot,
		tasks: []Task{
			{Description: "Take the dog for a walk (after I get a dog)"},
			{Description: "Go to the toilet"},
			{Description: "Allow TODOs to be marked completed or removed"},
		},
	}
}

func getEmbeddings(description string) ([]float32, error) {
	// Define the request payload
	payload := map[string]interface{}{
		"model": "bert-cpp-minilm-v6",
		"input": description,
	}

	// Marshal the payload into JSON
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// Make the HTTP request to the local OpenAI embeddings API
	resp, err := http.Post(localAI+"/embeddings", "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Check if the request was successful
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request to embeddings API failed with status code: %d", resp.StatusCode)
	}

	// Parse the response body
	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	// Return the embedding
	if len(result.Data) > 0 {
		return result.Data[0].Embedding, nil
	}
	return nil, errors.New("no embedding received from API")
}

type StoresSet struct {
	Store string `json:"store,omitempty" yaml:"store,omitempty"`

	Keys   [][]float32 `json:"keys" yaml:"keys"`
	Values []string    `json:"values" yaml:"values"`
}

func postTasksToExternalService(tasks []Task) error {
	keys := make([][]float32, 0, len(tasks))
	// Get the embeddings for the task description
	for _, task := range tasks {
		embedding, err := getEmbeddings(task.Description)
		if err != nil {
			return err
		}
		keys = append(keys, embedding)
	}

	values := make([]string, 0, len(tasks))
	for _, task := range tasks {
		values = append(values, task.Description)
	}

	// Construct the StoresSet object
	storesSet := StoresSet{
		Store:  "tasks_store", // Assuming you have a specific store name
		Keys:   keys,
		Values: values,
	}

	// Marshal the StoresSet object into JSON
	jsonData, err := json.Marshal(storesSet)
	if err != nil {
		return err
	}

	// Make the HTTP POST request to the external service
	resp, err := http.Post(localAI+"/stores/set", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check if the request was successful
	if resp.StatusCode != http.StatusOK {
		// read resp body into string
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return fmt.Errorf("store request failed with status code: %d: %s", resp.StatusCode, body)
	}

	return nil
}

type StoresFind struct {
	Store string `json:"store,omitempty" yaml:"store,omitempty"`

	Key  []float32 `json:"key" yaml:"key"`
	Topk int       `json:"topk" yaml:"topk"`
}

type StoresFindResponse struct {
	Keys         [][]float32 `json:"keys" yaml:"keys"`
	Values       []string    `json:"values" yaml:"values"`
	Similarities []float32   `json:"similarities" yaml:"similarities"`
}

func findSimilarTexts(inputText string, topk int) (StoresFindResponse, error) {
	// Initialize an empty response object
	response := StoresFindResponse{}

	// Get the embedding for the input text
	embedding, err := getEmbeddings(inputText)
	if err != nil {
		return response, err
	}

	// Construct the StoresFind object
	storesFind := StoresFind{
		Store: "tasks_store", // Assuming you have a specific store name
		Key:   embedding,
		Topk:  topk,
	}

	// Marshal the StoresFind object into JSON
	jsonData, err := json.Marshal(storesFind)
	if err != nil {
		return response, err
	}

	// Make the HTTP POST request to the external service's /stores/find endpoint
	resp, err := http.Post(localAI+"/stores/find", "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return response, err
	}
	defer resp.Body.Close()

	// Check if the request was successful
	if resp.StatusCode != http.StatusOK {
		return response, fmt.Errorf("request to /stores/find failed with status code: %d", resp.StatusCode)
	}

	// Parse the response body to retrieve similar texts and similarities
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return response, err
	}

	return response, nil
}

func (app *App) updateUI() {
	// Clear the flex layout
	app.flex.Clear()
	app.flex.SetDirection(tview.FlexColumn)
	app.flex.AddItem(nil, 0, 1, false)

	midCol := tview.NewFlex()
	midCol.SetDirection(tview.FlexRow)
	midCol.AddItem(nil, 0, 1, false)

	// Create a new table.
	app.table.Clear()
	app.table.SetBorders(true)

	// Set table headers
	app.table.SetCell(0, 0, tview.NewTableCell("Description").SetAlign(tview.AlignLeft).SetExpansion(1).SetAttributes(tcell.AttrBold))
	app.table.SetCell(0, 1, tview.NewTableCell("Similarity").SetAlign(tview.AlignCenter).SetExpansion(0).SetAttributes(tcell.AttrBold))

	// Add the tasks to the table.
	for i, task := range app.tasks {
		row := i + 1
		app.table.SetCell(row, 0, tview.NewTableCell(task.Description))
		app.table.SetCell(row, 1, tview.NewTableCell(fmt.Sprintf("%.2f", task.Similarity)))
	}

	if app.state == StateInput {
		inputField := tview.NewInputField()
		inputField.
			SetLabel("New Task: ").
			SetFieldWidth(0).
			SetDoneFunc(func(key tcell.Key) {
				if key == tcell.KeyEnter {
					task := Task{Description: inputField.GetText()}
					app.tasks = append(app.tasks, task)
					app.state = StateRoot
					err := postTasksToExternalService([]Task{task})
					if err != nil {
						panic(err)
					}
				}
				app.updateUI()
			})
		midCol.AddItem(inputField, 3, 2, true)
		app.app.SetFocus(inputField)
	} else if app.state == StateSearch {
		searchField := tview.NewInputField()
		searchField.SetLabel("Search: ").
			SetFieldWidth(0).
			SetDoneFunc(func(key tcell.Key) {
				if key == tcell.KeyEnter {
					similar, err := findSimilarTexts(searchField.GetText(), 100)
					if err != nil {
						panic(err)
					}
					app.tasks = make([]Task, len(similar.Keys))
					for i, v := range similar.Values {
						app.tasks[i] = Task{Description: v, Similarity: similar.Similarities[i]}
					}
				}
				app.updateUI()
			})
		midCol.AddItem(searchField, 3, 2, true)
		app.app.SetFocus(searchField)
	} else {
		midCol.AddItem(nil, 3, 1, false)
	}

	midCol.AddItem(app.table, 0, 2, true)

	// Add the status bar to the flex layout
	statusBar := tview.NewTextView().
		SetText(rootStatus).
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	if app.state == StateInput {
		statusBar.SetText(inputStatus)
	}
	midCol.AddItem(statusBar, 1, 1, false)
	midCol.AddItem(nil, 0, 1, false)

	app.flex.AddItem(midCol, 0, 10, true)
	app.flex.AddItem(nil, 0, 1, false)

	// Set the flex as the root element
	app.app.SetRoot(app.flex, true)
}

func main() {
	app := NewApp()
	tApp := tview.NewApplication()
	flex := tview.NewFlex().SetDirection(tview.FlexRow)
	table := tview.NewTable()

	app.app = tApp
	app.flex = flex
	app.table = table

	app.updateUI() // Initial UI setup

	app.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch app.state {
		case StateRoot:
			// Handle key events when in the root state
			switch event.Key() {
			case tcell.KeyRune:
				switch event.Rune() {
				case ' ':
					app.state = StateInput
					app.updateUI()
					return nil // Event is handled
				case '/':
					app.state = StateSearch
					app.updateUI()
					return nil // Event is handled
				}
			}

		case StateInput:
			// Handle key events when in the input state
			if event.Key() == tcell.KeyEsc {
				// Exit input state without adding a task
				app.state = StateRoot
				app.updateUI()
				return nil // Event is handled
			}

		case StateSearch:
			// Handle key events when in the search state
			if event.Key() == tcell.KeyEsc {
				// Exit search state
				app.state = StateRoot
				app.updateUI()
				return nil // Event is handled
			}
		}

		// Return the event for further processing by tview
		return event
	})

	if err := postTasksToExternalService(app.tasks); err != nil {
		panic(err)
	}

	// Start the application
	if err := app.app.Run(); err != nil {
		panic(err)
	}
}
