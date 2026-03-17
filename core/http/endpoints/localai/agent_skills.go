package localai

import (
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	skilldomain "github.com/mudler/skillserver/pkg/domain"
)

type skillResponse struct {
	Name          string            `json:"name"`
	Content       string            `json:"content"`
	Description   string            `json:"description,omitempty"`
	License       string            `json:"license,omitempty"`
	Compatibility string            `json:"compatibility,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	AllowedTools  string            `json:"allowed-tools,omitempty"`
	ReadOnly      bool              `json:"readOnly"`
}

func skillToResponse(s skilldomain.Skill) skillResponse {
	out := skillResponse{Name: s.Name, Content: s.Content, ReadOnly: s.ReadOnly}
	if s.Metadata != nil {
		out.Description = s.Metadata.Description
		out.License = s.Metadata.License
		out.Compatibility = s.Metadata.Compatibility
		out.Metadata = s.Metadata.Metadata
		out.AllowedTools = s.Metadata.AllowedTools.String()
	}
	return out
}

func skillsToResponses(skills []skilldomain.Skill) []skillResponse {
	out := make([]skillResponse, len(skills))
	for i, s := range skills {
		out[i] = skillToResponse(s)
	}
	return out
}

func ListSkillsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		skills, err := svc.ListSkills()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, skillsToResponses(skills))
	}
}

func GetSkillsConfigEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		cfg := svc.GetSkillsConfig()
		return c.JSON(http.StatusOK, cfg)
	}
}

func SearchSkillsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		query := c.QueryParam("q")
		skills, err := svc.SearchSkills(query)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, skillsToResponses(skills))
	}
}

func CreateSkillEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		var payload struct {
			Name          string            `json:"name"`
			Description   string            `json:"description"`
			Content       string            `json:"content"`
			License       string            `json:"license,omitempty"`
			Compatibility string            `json:"compatibility,omitempty"`
			AllowedTools  string            `json:"allowed-tools,omitempty"`
			Metadata      map[string]string `json:"metadata,omitempty"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		skill, err := svc.CreateSkill(payload.Name, payload.Description, payload.Content, payload.License, payload.Compatibility, payload.AllowedTools, payload.Metadata)
		if err != nil {
			if strings.Contains(err.Error(), "already exists") {
				return c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, skillToResponse(*skill))
	}
}

func GetSkillEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		skill, err := svc.GetSkill(c.Param("name"))
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, skillToResponse(*skill))
	}
}

func UpdateSkillEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		var payload struct {
			Description   string            `json:"description"`
			Content       string            `json:"content"`
			License       string            `json:"license,omitempty"`
			Compatibility string            `json:"compatibility,omitempty"`
			AllowedTools  string            `json:"allowed-tools,omitempty"`
			Metadata      map[string]string `json:"metadata,omitempty"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		skill, err := svc.UpdateSkill(c.Param("name"), payload.Description, payload.Content, payload.License, payload.Compatibility, payload.AllowedTools, payload.Metadata)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, skillToResponse(*skill))
	}
}

func DeleteSkillEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		if err := svc.DeleteSkill(c.Param("name")); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func ExportSkillEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		// The wildcard param captures the path after /export/
		name := c.Param("*")
		data, err := svc.ExportSkill(name)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		c.Response().Header().Set("Content-Disposition", "attachment; filename="+name+".tar.gz")
		c.Response().Header().Set("Content-Type", "application/gzip")
		return c.Blob(http.StatusOK, "application/gzip", data)
	}
}

func ImportSkillEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		file, err := c.FormFile("file")
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "file required"})
		}
		src, err := file.Open()
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		defer src.Close()
		data, err := io.ReadAll(src)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		skill, err := svc.ImportSkill(data)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, skill)
	}
}

// --- Skill Resources ---

func ListSkillResourcesEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		resources, skill, err := svc.ListSkillResources(c.Param("name"))
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		scripts := []map[string]any{}
		references := []map[string]any{}
		assets := []map[string]any{}
		for _, res := range resources {
			m := map[string]any{
				"path":      res.Path,
				"name":      res.Name,
				"size":      res.Size,
				"mime_type": res.MimeType,
				"readable":  res.Readable,
				"modified":  res.Modified.Format("2006-01-02T15:04:05Z07:00"),
			}
			switch res.Type {
			case "script":
				scripts = append(scripts, m)
			case "reference":
				references = append(references, m)
			case "asset":
				assets = append(assets, m)
			}
		}
		return c.JSON(http.StatusOK, map[string]any{
			"scripts":    scripts,
			"references": references,
			"assets":     assets,
			"readOnly":   skill.ReadOnly,
		})
	}
}

func GetSkillResourceEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		content, info, err := svc.GetSkillResource(c.Param("name"), c.Param("*"))
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		if c.QueryParam("encoding") == "base64" || !info.Readable {
			return c.JSON(http.StatusOK, map[string]any{
				"content":   content.Content,
				"encoding":  content.Encoding,
				"mime_type": content.MimeType,
				"size":      content.Size,
			})
		}
		c.Response().Header().Set("Content-Type", content.MimeType)
		return c.String(http.StatusOK, content.Content)
	}
}

func CreateSkillResourceEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		file, err := c.FormFile("file")
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "file is required"})
		}
		path := c.FormValue("path")
		if path == "" {
			path = file.Filename
		}
		src, err := file.Open()
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "failed to open file"})
		}
		defer src.Close()
		data, err := io.ReadAll(src)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if err := svc.CreateSkillResource(c.Param("name"), path, data); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, map[string]string{"path": path})
	}
}

func UpdateSkillResourceEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		var payload struct {
			Content string `json:"content"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := svc.UpdateSkillResource(c.Param("name"), c.Param("*"), payload.Content); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func DeleteSkillResourceEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		if err := svc.DeleteSkillResource(c.Param("name"), c.Param("*")); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

// --- Git Repos ---

func ListGitReposEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		repos, err := svc.ListGitRepos()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, repos)
	}
}

func AddGitRepoEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		var payload struct {
			URL string `json:"url"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		repo, err := svc.AddGitRepo(payload.URL)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, repo)
	}
}

func UpdateGitRepoEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		var payload struct {
			URL     string `json:"url"`
			Enabled *bool  `json:"enabled"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		repo, err := svc.UpdateGitRepo(c.Param("id"), payload.URL, payload.Enabled)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, repo)
	}
}

func DeleteGitRepoEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		if err := svc.DeleteGitRepo(c.Param("id")); err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func SyncGitRepoEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		if err := svc.SyncGitRepo(c.Param("id")); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusAccepted, map[string]string{"status": "syncing"})
	}
}

func ToggleGitRepoEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		repo, err := svc.ToggleGitRepo(c.Param("id"))
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, repo)
	}
}
