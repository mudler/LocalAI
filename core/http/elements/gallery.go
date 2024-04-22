package elements

import (
	"fmt"

	"github.com/chasefleming/elem-go"
	"github.com/chasefleming/elem-go/attrs"
	"github.com/go-skynet/LocalAI/pkg/gallery"
)

func DoneProgress(uid string) string {
	return elem.Div(
		attrs.Props{},
		elem.H3(
			attrs.Props{
				"role":      "status",
				"id":        "pblabel",
				"tabindex":  "-1",
				"autofocus": "",
			},
			elem.Text("Installation completed"),
		),
	).Render()
}

func ProgressBar(progress string) string {
	return elem.Div(attrs.Props{
		"class":           "progress",
		"role":            "progressbar",
		"aria-valuemin":   "0",
		"aria-valuemax":   "100",
		"aria-valuenow":   "0",
		"aria-labelledby": "pblabel",
	},
		elem.Div(attrs.Props{
			"id":    "pb",
			"class": "progress-bar",
			"style": "width:" + progress + "%",
		}),
	).Render()
}

func StartProgressBar(uid, progress string) string {
	if progress == "" {
		progress = "0"
	}
	return elem.Div(attrs.Props{
		"hx-trigger": "done",
		"hx-get":     "/browse/job/" + uid,
		"hx-swap":    "outerHTML",
		"hx-target":  "this",
	},
		elem.H3(
			attrs.Props{
				"role":      "status",
				"id":        "pblabel",
				"tabindex":  "-1",
				"autofocus": "",
			},
			elem.Text("Installing"),
			// This is a simple example of how to use the HTMLX library to create a progress bar that updates every 600ms.
			elem.Div(attrs.Props{
				"hx-get":     "/browse/job/progress/" + uid,
				"hx-trigger": "every 600ms",
				"hx-target":  "this",
				"hx-swap":    "innerHTML",
			},
				elem.Raw(ProgressBar(progress)),
			),
		),
	).Render()
}

// /
// <div class="max-w-sm rounded overflow-hidden shadow-lg">
//
//	<img class="w-full" src="/img/card-top.jpg" alt="Sunset in the mountains">
//	<div class="px-6 py-4">
//	  <div class="font-bold text-xl mb-2">The Coldest Sunset</div>
//	  <p class="text-gray-700 text-base">
//	    Lorem ipsum dolor sit amet, consectetur adipisicing elit. Voluptatibus quia, nulla! Maiores et perferendis eaque, exercitationem praesentium nihil.
//	  </p>
//	</div>
//	<div class="px-6 pt-4 pb-2">
//	  <span class="inline-block bg-gray-200 rounded-full px-3 py-1 text-sm font-semibold text-gray-700 mr-2 mb-2">#photography</span>
//	  <span class="inline-block bg-gray-200 rounded-full px-3 py-1 text-sm font-semibold text-gray-700 mr-2 mb-2">#travel</span>
//	  <span class="inline-block bg-gray-200 rounded-full px-3 py-1 text-sm font-semibold text-gray-700 mr-2 mb-2">#winter</span>
//	</div>
//
// </div>
// /
func ListModels(models []*gallery.GalleryModel) string {
	modelsElements := []elem.Node{}
	span := func(s string) elem.Node {
		return elem.Span(
			attrs.Props{
				"class": "float-right inline-block bg-green-500 text-white py-1 px-3 rounded-full text-xs",
			},
			elem.Text(s),
		)
	}
	installButton := func(m *gallery.GalleryModel) elem.Node {
		return elem.Button(
			attrs.Props{
				"class": "float-right inline-block rounded bg-primary px-6 pb-2 pt-2.5 text-xs font-medium uppercase leading-normal text-white shadow-primary-3 transition duration-150 ease-in-out hover:bg-primary-accent-300 hover:shadow-primary-2 focus:bg-primary-accent-300 focus:shadow-primary-2 focus:outline-none focus:ring-0 active:bg-primary-600 active:shadow-primary-2 dark:shadow-black/30 dark:hover:shadow-dark-strong dark:focus:shadow-dark-strong dark:active:shadow-dark-strong",
				// post the Model ID as param
				"hx-post": "/browse/install/model/" + fmt.Sprintf("%s@%s", m.Gallery.Name, m.Name),
			},
			elem.Text("Install"),
		)
	}

	descriptionDiv := func(m *gallery.GalleryModel) elem.Node {

		return elem.Div(
			attrs.Props{
				"class": "p-6",
			},
			elem.H5(
				attrs.Props{
					"class": "mb-2 text-xl font-medium leading-tight",
				},
				elem.Text(m.Name),
			),
			elem.P(
				attrs.Props{
					"class": "mb-4 text-base",
				},
				elem.Text(m.Description),
			),
		)
	}

	actionDiv := func(m *gallery.GalleryModel) elem.Node {
		return elem.Div(
			attrs.Props{
				"class": "px-6 pt-4 pb-2",
			},
			elem.Span(
				attrs.Props{
					"class": "inline-block bg-gray-200 rounded-full px-3 py-1 text-sm font-semibold text-gray-700 mr-2 mb-2",
				},
				elem.Text("Repository: "+m.Gallery.Name),
			),
			elem.If(m.Installed, span("Installed"), installButton(m)),
		)
	}

	for _, m := range models {
		modelsElements = append(modelsElements,
			elem.Div(
				attrs.Props{
					"class": "me-4 mb-2 block rounded-lg bg-white shadow-secondary-1  dark:bg-gray-800 dark:bg-surface-dark dark:text-white text-surface p-2",
				},
				elem.Div(
					attrs.Props{
						"class": "p-6",
					},
					descriptionDiv(m),
					actionDiv(m),
				//	elem.If(m.Installed, span("Installed"), installButton(m)),

				//	elem.If(m.Installed, span("Installed"), span("Not Installed")),
				),
			),
		)
	}

	wrapper := elem.Div(attrs.Props{
		"class": "dark grid grid-cols-1 grid-rows-1 md:grid-cols-2 ",
	}, modelsElements...)

	return wrapper.Render()
}
