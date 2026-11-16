package localaitools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// projectsActions lists the actions of the projects tool in the order the
// schema shows them. projectsReads are the ones that change nothing: the
// read-only server keeps only those.
var (
	projectsActions = []string{ActionList, ActionGet, ActionCreate, ActionUpdate, ActionArchive, ActionRestore,
		ActionDelete, ActionSetChatProject, ActionPull, ActionUnpull, ActionAgents,
		ActionConnections, ActionTriggers, ActionLoops, ActionShared, ActionSharedGet,
		ActionMemory, ActionWriteMemory, ActionChats}
	projectsReads = []string{ActionList, ActionGet, ActionAgents, ActionConnections, ActionTriggers, ActionLoops, ActionShared,
		ActionSharedGet, ActionMemory, ActionChats}
)

// projectsAction serves the projects tool: it checks the fields each action
// needs, with an error that names the tool, the action and the field, and
// sends the action to the client.
func projectsAction(ctx context.Context, client LocalAIClient, in ProjectsToolInput) (any, error) {
	if in.Action == "" {
		return nil, fmt.Errorf("projects: action is required (%s)", strings.Join(projectsActions, ", "))
	}
	var err error
	switch in.Action {
	case ActionList, ActionShared:
	case ActionGet, ActionAgents, ActionConnections, ActionTriggers, ActionLoops, ActionChats, ActionMemory,
		ActionSharedGet, ActionArchive, ActionRestore, ActionDelete:
		err = need(ToolProjects, in.Action, [2]string{"project", in.Project})
	case ActionWriteMemory:
		if err = need(ToolProjects, in.Action, [2]string{"project", in.Project}, [2]string{"file", in.File}); err == nil && in.Content == nil {
			err = errors.New(`projects: action "write_memory" needs content (the full new text of the file)`)
		}
	case ActionCreate:
		err = need(ToolProjects, in.Action, [2]string{"name", in.Name})
	case ActionUpdate:
		if err = need(ToolProjects, in.Action, [2]string{"project", in.Project}); err == nil && in.Name == "" && in.Description == nil && in.Attached == nil {
			err = errors.New(`projects: action "update" needs name, description or attached`)
		}
	case ActionSetChatProject:
		// An empty project takes the chat out of its project.
		err = need(ToolProjects, in.Action, [2]string{"chat_id", in.ChatID})
	case ActionPull, ActionUnpull:
		err = need(ToolProjects, in.Action, [2]string{"chat_id", in.ChatID}, [2]string{"project", in.Project})
	default:
		return nil, fmt.Errorf("projects: unknown action %q (use %s)", in.Action, strings.Join(projectsActions, ", "))
	}
	if err != nil {
		return nil, err
	}
	attached, err := ProjectAttachedFromMap(in.Attached)
	if err != nil {
		return nil, err
	}
	return client.Projects(ctx, ProjectsRequest{Action: in.Action, Project: strings.TrimSpace(in.Project),
		Name: in.Name, Description: in.Description, ChatID: strings.TrimSpace(in.ChatID),
		File: strings.TrimSpace(in.File), Content: in.Content, Attached: attached})
}

// apiProject is what the project routes answer.
type apiProject struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Status      string `json:"status"`
	ChatCount   int    `json:"chat_count"`
	AgentCount  int    `json:"agent_count"`
	// Attached is what the project attaches, in the shape of its defaults.
	Attached ProjectAttached `json:"attached"`
}

func (p apiProject) info() ProjectInfo {
	return ProjectInfo{ID: p.ID, Name: p.Name, Slug: p.Slug, Description: p.Description, Status: p.Status, ChatCount: p.ChatCount, AgentCount: p.AgentCount, Attached: p.Attached}
}

// Projects runs one action of the projects tool through the project routes
// as the acting user: the API owns the rules (ownership, archived projects,
// a chat that is running), and this code only maps the action to the route.
func (i *Inference) Projects(ctx context.Context, req ProjectsRequest) (*ProjectsResult, error) {
	spec := clientSpecAction(ToolProjects, req.Action)
	if spec.Tool == "" {
		return nil, fmt.Errorf("projects: unknown action %q", req.Action)
	}
	id, chat := url.PathEscape(req.Project), url.PathEscape(req.ChatID)
	switch req.Action {
	case ActionList:
		data, err := i.call(ctx, spec, http.MethodGet, spec.Route, "", nil)
		if err != nil {
			return nil, err
		}
		var out struct {
			Projects []apiProject `json:"projects"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("read the projects: %w", err)
		}
		res := &ProjectsResult{Projects: make([]ProjectInfo, 0, len(out.Projects))}
		for _, p := range out.Projects {
			res.Projects = append(res.Projects, p.info())
		}
		return res, nil
	case ActionShared:
		data, err := i.call(ctx, spec, http.MethodGet, spec.Route, "", nil)
		if err != nil {
			return nil, err
		}
		var out struct {
			Projects []SharedProjectInfo `json:"projects"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("read the shared projects: %w", err)
		}
		res := &ProjectsResult{Shared: make([]SharedProjectInfo, 0, len(out.Projects))}
		for _, p := range out.Projects {
			// Only these fields reach the model, whatever the route adds.
			res.Shared = append(res.Shared, SharedProjectInfo{Ref: p.Ref, Name: p.Name, Mode: p.Mode, Status: p.Status, OwnerName: p.OwnerName})
		}
		return res, nil
	case ActionSharedGet:
		data, err := i.call(ctx, spec, http.MethodGet, "/api/chats/projects/shared/"+id, "", nil)
		if err != nil {
			return nil, err
		}
		var p SharedProjectInfo
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, fmt.Errorf("read the shared project: %w", err)
		}
		res := &ProjectsResult{SharedOne: &SharedProjectInfo{Ref: p.Ref, Name: p.Name, Mode: p.Mode, Status: p.Status, OwnerName: p.OwnerName}}
		// The memory is read as the person through the grant. An archived or
		// broken project has none to read: the project is still shown.
		if p.Status != "active" {
			return res, nil
		}
		mem, err := i.call(ctx, clientSpecAction(ToolProjects, actionSharedMemory), http.MethodGet, "/api/chats/projects/shared/"+id+"/memory", "", nil)
		if err != nil {
			return nil, err
		}
		m, err := readProjectMemory(mem, false)
		if err != nil {
			return nil, err
		}
		res.Memory = m.Memory
		return res, nil
	case ActionMemory:
		data, err := i.call(ctx, spec, http.MethodGet, "/api/chats/projects/"+id+"/memory", "", nil)
		if err != nil {
			return nil, err
		}
		return readProjectMemory(data, true)
	case ActionWriteMemory:
		if req.Content == nil {
			return nil, errors.New(`projects: action "write_memory" needs content`)
		}
		if _, err := i.sendJSON(ctx, spec, http.MethodPut, "/api/chats/projects/"+id+"/memory/"+url.PathEscape(req.File), map[string]any{"content": *req.Content}); err != nil {
			return nil, err
		}
		return &ProjectsResult{Written: req.File}, nil
	case ActionChats:
		data, err := i.call(ctx, spec, http.MethodGet, "/api/chats/projects/"+id+"/chats", "", nil)
		if err != nil {
			return nil, err
		}
		var out struct {
			Conversations []struct {
				ID      string `json:"id"`
				Title   string `json:"title"`
				AgentID string `json:"agent_id"`
				Updated string `json:"updated"`
			} `json:"conversations"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("read the chats: %w", err)
		}
		res := &ProjectsResult{Chats: make([]ProjectChatInfo, 0, len(out.Conversations))}
		for _, c := range out.Conversations {
			res.Chats = append(res.Chats, ProjectChatInfo{ID: c.ID, Title: c.Title, AgentID: c.AgentID, Updated: c.Updated})
		}
		return res, nil
	case ActionAgents:
		data, err := i.call(ctx, spec, http.MethodGet, "/api/chats/projects/"+id+"/agents", "", nil)
		if err != nil {
			return nil, err
		}
		var out struct {
			Agents []ProjectAgentInfo `json:"agents"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("read the agents: %w", err)
		}
		return &ProjectsResult{Agents: out.Agents}, nil
	case ActionConnections, ActionTriggers, ActionLoops:
		data, err := i.call(ctx, spec, http.MethodGet, "/api/chats/projects/"+id+"/"+req.Action, "", nil)
		if err != nil {
			return nil, err
		}
		var out ProjectsResult
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("read the %s: %w", req.Action, err)
		}
		return &ProjectsResult{Connections: out.Connections, Triggers: out.Triggers, Loops: out.Loops}, nil
	case ActionGet:
		return i.projectCall(ctx, spec, http.MethodGet, "/api/chats/projects/"+id, nil)
	case ActionCreate:
		return i.projectCall(ctx, spec, http.MethodPost, spec.Route, projectBody(req))
	case ActionUpdate:
		return i.projectCall(ctx, spec, http.MethodPatch, "/api/chats/projects/"+id, projectBody(req))
	case ActionArchive, ActionRestore:
		return i.projectCall(ctx, spec, http.MethodPost, "/api/chats/projects/"+id+"/"+req.Action, nil)
	case ActionDelete:
		if _, err := i.call(ctx, spec, http.MethodDelete, "/api/chats/projects/"+id, "", nil); err != nil {
			return nil, err
		}
		return &ProjectsResult{Deleted: true}, nil
	case ActionSetChatProject:
		return i.chatCall(ctx, spec, "/api/chats/"+chat+"/project", map[string]any{"project_id": req.Project})
	case ActionPull, ActionUnpull:
		// The API replaces the whole list, so read the chat's list first and
		// write it back with this project added or removed.
		data, err := i.call(ctx, clientSpecAction(ToolProjects, actionChat), http.MethodGet, "/api/chats/"+chat, "", nil)
		if err != nil {
			return nil, err
		}
		var cur ProjectChat
		if err := json.Unmarshal(data, &cur); err != nil {
			return nil, fmt.Errorf("read the chat: %w", err)
		}
		ids := slices.DeleteFunc(slices.Clone(cur.PulledProjects), func(p string) bool { return p == req.Project })
		if req.Action == ActionPull {
			ids = append(ids, req.Project)
		}
		if ids == nil {
			ids = []string{}
		}
		return i.chatCall(ctx, spec, "/api/chats/"+chat+"/projects", map[string]any{"project_ids": ids})
	}
	return nil, fmt.Errorf("projects: unknown action %q", req.Action)
}

// readProjectMemory reads the answer of a memory route. The owner's route
// lists topic files as rows with a size and a time, the shared route as names;
// only the names reach the model.
func readProjectMemory(data []byte, owner bool) (*ProjectsResult, error) {
	var raw struct {
		Index string          `json:"index"`
		Files json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("read the memory: %w", err)
	}
	mem := &ProjectMemoryInfo{Index: raw.Index, Files: []string{}}
	if owner {
		var rows []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw.Files, &rows); err != nil {
			return nil, fmt.Errorf("read the memory files: %w", err)
		}
		for _, r := range rows {
			mem.Files = append(mem.Files, r.Name)
		}
	} else if err := json.Unmarshal(raw.Files, &mem.Files); err != nil {
		return nil, fmt.Errorf("read the memory files: %w", err)
	}
	return &ProjectsResult{Memory: mem}, nil
}

func projectBody(req ProjectsRequest) map[string]any {
	body := map[string]any{}
	if req.Name != "" {
		body["name"] = req.Name
	}
	if req.Description != nil {
		body["description"] = *req.Description
	}
	if req.Attached != nil {
		body["attached"] = req.Attached
	}
	return body
}

func (i *Inference) projectCall(ctx context.Context, spec ClientToolSpec, method, route string, body any) (*ProjectsResult, error) {
	data, err := i.sendJSON(ctx, spec, method, route, body)
	if err != nil {
		return nil, err
	}
	var p apiProject
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("read the project: %w", err)
	}
	info := p.info()
	return &ProjectsResult{Project: &info}, nil
}

func (i *Inference) chatCall(ctx context.Context, spec ClientToolSpec, route string, body any) (*ProjectsResult, error) {
	data, err := i.sendJSON(ctx, spec, http.MethodPut, route, body)
	if err != nil {
		return nil, err
	}
	var c ProjectChat
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("read the chat: %w", err)
	}
	return &ProjectsResult{Chat: &c}, nil
}

// sendJSON sends a request with an optional JSON body and returns the answer.
func (i *Inference) sendJSON(ctx context.Context, spec ClientToolSpec, method, route string, body any) ([]byte, error) {
	if body == nil {
		return i.call(ctx, spec, method, route, "", nil)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return i.call(ctx, spec, method, route, "application/json", raw)
}

// projectsStatus words an API refusal for the model: these routes have no
// model, and the status tells what to do.
func projectsStatus(e *StatusError) error {
	switch e.Status {
	case http.StatusForbidden:
		return errors.New("projects: the server refused it (403): this account may not use the chats, or the agents, connectors or triggers that the action lists")
	case http.StatusNotFound:
		return errors.New("projects: no such project or chat (404). Use list for the projects of this account")
	case http.StatusConflict:
		return fmt.Errorf("projects: %s. A chat that is running cannot change its project: wait until it is idle", e.Detail)
	case http.StatusBadRequest:
		return fmt.Errorf("projects: the server rejected it: %s", e.Detail)
	}
	return e
}
