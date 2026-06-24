package routes

import (
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/services/modeladmin"
)

func modelRevisionLifecycleFor(app *application.Application) modeladmin.ModelRevisionLifecycle {
	if app == nil || app.Distributed() == nil {
		if app == nil {
			return nil
		}
		return modeladmin.NewLocalModelRevisionLifecycle(app.ModelLoader())
	}
	distributed := app.Distributed()
	return modeladmin.NewDistributedModelRevisionLifecycle(distributed.Registry, distributed.ModelCleanup)
}
