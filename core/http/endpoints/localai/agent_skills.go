package localai

import (
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	skillsManager "github.com/mudler/LocalAI/core/services/skills"
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

// getSkillManager returns a SkillManager for the request's user.
func getSkillManager(c echo.Context, app *application.Application) (skillsManager.Manager, error) {
	svc := app.AgentPoolService()
	userID := getUserID(c)
	return svc.SkillManagerForUser(userID)
}

func getSkillManagerEffective(c echo.Context, app *application.Application) (skillsManager.Manager, error) {
	svc := app.AgentPoolService()
	userID := effectiveUserID(c)
	return svc.SkillManagerForUser(userID)
}

func ListSkillsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		skills, err := mgr.List()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		// Admin cross-user aggregation
		if wantsAllUsers(c) {
			svc := app.AgentPoolService()
			usm := svc.UserServicesManager()
			if usm != nil {
				userIDs, _ := usm.ListAllUserIDs()
				userGroups := map[string]any{}
				userID := getUserID(c)
				for _, uid := range userIDs {
					if uid == userID {
						continue
					}
					uidMgr, mgrErr := svc.SkillManagerForUser(uid)
					if mgrErr != nil {
						continue
					}
					userSkills, listErr := uidMgr.List()
					if listErr != nil || len(userSkills) == 0 {
						continue
					}
					userGroups[uid] = map[string]any{"skills": skillsToResponses(userSkills)}
				}
				resp := map[string]any{
					"skills": skillsToResponses(skills),
				}
				if len(userGroups) > 0 {
					resp["user_groups"] = userGroups
				}
				return c.JSON(http.StatusOK, resp)
			}
		}

		return c.JSON(http.StatusOK, map[string]any{"skills": skillsToResponses(skills)})
	}
}

func GetSkillsConfigEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusOK, map[string]string{})
		}
		return c.JSON(http.StatusOK, mgr.GetConfig())
	}
}

func SearchSkillsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		query := c.QueryParam("q")
		skills, err := mgr.Search(query)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, skillsToResponses(skills))
	}
}

func CreateSkillEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
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
		skill, err := mgr.Create(payload.Name, payload.Description, payload.Content, payload.License, payload.Compatibility, payload.AllowedTools, payload.Metadata)
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
		mgr, err := getSkillManagerEffective(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		skill, err := mgr.Get(c.Param("name"))
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, skillToResponse(*skill))
	}
}

func UpdateSkillEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		mgr, err := getSkillManagerEffective(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
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
		skill, err := mgr.Update(c.Param("name"), payload.Description, payload.Content, payload.License, payload.Compatibility, payload.AllowedTools, payload.Metadata)
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
		mgr, err := getSkillManagerEffective(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if err := mgr.Delete(c.Param("name")); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func ExportSkillEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		mgr, err := getSkillManagerEffective(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		name := c.Param("*")
		data, err := mgr.Export(name)
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
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
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
		skill, err := mgr.Import(data)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, skill)
	}
}

// --- Skill Resources ---

func ListSkillResourcesEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		mgr, err := getSkillManagerEffective(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		resources, skill, err := mgr.ListResources(c.Param("name"))
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
		mgr, err := getSkillManagerEffective(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		content, info, err := mgr.GetResource(c.Param("name"), c.Param("*"))
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
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		file, fileErr := c.FormFile("file")
		if fileErr != nil {
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
		if err := mgr.CreateResource(c.Param("name"), path, data); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, map[string]string{"path": path})
	}
}

func UpdateSkillResourceEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		var payload struct {
			Content string `json:"content"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := mgr.UpdateResource(c.Param("name"), c.Param("*"), payload.Content); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func DeleteSkillResourceEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if err := mgr.DeleteResource(c.Param("name"), c.Param("*")); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

// --- Git Repos ---

func ListGitReposEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		repos, err := mgr.ListGitRepos()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, repos)
	}
}

func AddGitRepoEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		var payload struct {
			URL string `json:"url"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		repo, err := mgr.AddGitRepo(payload.URL)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, repo)
	}
}

func UpdateGitRepoEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		var payload struct {
			URL     string `json:"url"`
			Enabled *bool  `json:"enabled"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		repo, err := mgr.UpdateGitRepo(c.Param("id"), payload.URL, payload.Enabled)
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
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if err := mgr.DeleteGitRepo(c.Param("id")); err != nil {
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
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		if err := mgr.SyncGitRepo(c.Param("id")); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusAccepted, map[string]string{"status": "syncing"})
	}
}

func ToggleGitRepoEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		mgr, err := getSkillManager(c, app)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		repo, err := mgr.ToggleGitRepo(c.Param("id"))
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, repo)
	}
}
