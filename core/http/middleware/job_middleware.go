package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/jobs"
)

//Find model name from JSON body
type simpleModelRequest struct{
	Model	string	`json:"model"`
}

//Track jobs by intercepting requests
func JobTracker() echo.MiddlewareFunc{
	return func(next echo.HandlerFunc) echo.HandlerFunc{
		return func(c echo.Context) error{
			req:=c.Request()
			urlPath:=req.URL.Path

			//Filter
			if !strings.HasPrefix(urlPath,"/v1/") && !strings.HasPrefix(urlPath,"/api/"){
				return next(c)
			}
			if strings.Contains(urlPath,"/jobs"){
				return next(c)
			}

			modelName:="unknown"

			if req.Method=="POST"{
				bodyBytes,_:=io.ReadAll(req.Body)
				req.Body=io.NopCloser(bytes.NewBuffer(bodyBytes))

				var tmp simpleModelRequest
				if err:=json.Unmarshal(bodyBytes,&tmp);err==nil && tmp.Model!=""{
					modelName=tmp.Model
				}
			}
			job:=jobs.CreateJob(urlPath,modelName,c.RealIP())
			store:=jobs.GetStore()
			store.AddJob(job)

			err:=next(c)

			if err!=nil{
				store.UpdateStatus(job.ID,"error")
			}else{
				store.UpdateStatus(job.ID,"finished")
			}
			return err
		}
	}
}