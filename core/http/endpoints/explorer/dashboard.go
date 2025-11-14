package explorer

import (
	"encoding/base64"
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/explorer"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/internal"
)

func Dashboard() echo.HandlerFunc {
	return func(c echo.Context) error {
		summary := map[string]interface{}{
			"Title":   "LocalAI API - " + internal.PrintableVersion(),
			"Version": internal.PrintableVersion(),
			"BaseURL": middleware.BaseURL(c),
		}

		contentType := c.Request().Header.Get("Content-Type")
		accept := c.Request().Header.Get("Accept")
		if strings.Contains(contentType, "application/json") || (accept != "" && !strings.Contains(accept, "html")) {
			// The client expects a JSON response
			return c.JSON(http.StatusOK, summary)
		} else {
			// Render index
			return c.Render(http.StatusOK, "views/explorer", summary)
		}
	}
}

type AddNetworkRequest struct {
	Token       string `json:"token"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Network struct {
	explorer.TokenData
	Token string `json:"token"`
}

func ShowNetworks(db *explorer.Database) echo.HandlerFunc {
	return func(c echo.Context) error {
		results := []Network{}
		for _, token := range db.TokenList() {
			networkData, exists := db.Get(token) // get the token data
			hasWorkers := false
			for _, cluster := range networkData.Clusters {
				if len(cluster.Workers) > 0 {
					hasWorkers = true
					break
				}
			}
			if exists && hasWorkers {
				results = append(results, Network{TokenData: networkData, Token: token})
			}
		}

		// order by number of clusters
		sort.Slice(results, func(i, j int) bool {
			return len(results[i].Clusters) > len(results[j].Clusters)
		})

		return c.JSON(http.StatusOK, results)
	}
}

func AddNetwork(db *explorer.Database) echo.HandlerFunc {
	return func(c echo.Context) error {
		request := new(AddNetworkRequest)
		if err := c.Bind(request); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{"error": "Cannot parse JSON"})
		}

		if request.Token == "" {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{"error": "Token is required"})
		}

		if request.Name == "" {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{"error": "Name is required"})
		}

		if request.Description == "" {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{"error": "Description is required"})
		}

		// TODO: check if token is valid, otherwise reject
		// try to decode the token from base64
		_, err := base64.StdEncoding.DecodeString(request.Token)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{"error": "Invalid token"})
		}

		if _, exists := db.Get(request.Token); exists {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{"error": "Token already exists"})
		}
		err = db.Set(request.Token, explorer.TokenData{Name: request.Name, Description: request.Description})
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{"error": "Cannot add token"})
		}

		return c.JSON(http.StatusOK, map[string]interface{}{"message": "Token added"})
	}
}
