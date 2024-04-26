package elements

import (
	"fmt"

	"github.com/chasefleming/elem-go"
	"github.com/chasefleming/elem-go/attrs"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/go-skynet/LocalAI/pkg/xsync"
)

const (
	NoImage = "https://upload.wikimedia.org/wikipedia/commons/6/65/No-Image-Placeholder.svg"
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

func ErrorProgress(err string) string {
	return elem.Div(
		attrs.Props{},
		elem.H3(
			attrs.Props{
				"role":      "status",
				"id":        "pblabel",
				"tabindex":  "-1",
				"autofocus": "",
			},
			elem.Text("Error"+err),
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

func cardSpan(text, icon string) elem.Node {
	return elem.Span(
		attrs.Props{
			"class": "inline-block bg-gray-200 rounded-full px-3 py-1 text-sm font-semibold text-gray-700 mr-2 mb-2",
		},
		elem.I(attrs.Props{
			"class": icon + " pr-2",
		}),
		elem.Text(text),
	)
}

func ListModels(models []*gallery.GalleryModel, installing *xsync.SyncedMap[string, string]) string {
	//StartProgressBar(uid, "0")
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
				"data-twe-ripple-init":  "",
				"data-twe-ripple-color": "light",
				"class":                 "float-right inline-block rounded bg-primary px-6 pb-2.5 mb-3 pt-2.5 text-xs font-medium uppercase leading-normal text-white shadow-primary-3 transition duration-150 ease-in-out hover:bg-primary-accent-300 hover:shadow-primary-2 focus:bg-primary-accent-300 focus:shadow-primary-2 focus:outline-none focus:ring-0 active:bg-primary-600 active:shadow-primary-2 dark:shadow-black/30 dark:hover:shadow-dark-strong dark:focus:shadow-dark-strong dark:active:shadow-dark-strong",
				"hx-swap":               "outerHTML",
				// post the Model ID as param
				"hx-post": "/browse/install/model/" + fmt.Sprintf("%s@%s", m.Gallery.Name, m.Name),
			},
			elem.I(
				attrs.Props{
					"class": "fa-solid fa-download pr-2",
				},
			),
			elem.Text("Install"),
		)
	}

	descriptionDiv := func(m *gallery.GalleryModel) elem.Node {

		return elem.Div(
			attrs.Props{
				"class": "p-6 text-surface dark:text-white",
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
		galleryID := fmt.Sprintf("%s@%s", m.Gallery.Name, m.Name)
		currentlyInstalling := installing.Exists(galleryID)

		nodes := []elem.Node{
			cardSpan("Repository: "+m.Gallery.Name, "fa-brands fa-git-alt"),
		}

		if m.License != "" {
			nodes = append(nodes,
				cardSpan("License: "+m.License, "fas fa-book"),
			)
		}

		for _, tag := range m.Tags {
			nodes = append(nodes,
				cardSpan(tag, "fas fa-tag"),
			)
		}

		for i, url := range m.URLs {
			nodes = append(nodes,
				elem.A(
					attrs.Props{
						"class":  "inline-block bg-gray-200 rounded-full px-3 py-1 text-sm font-semibold text-gray-700 mr-2 mb-2",
						"href":   url,
						"target": "_blank",
					},
					elem.I(attrs.Props{
						"class": "fas fa-link pr-2",
					}),
					elem.Text("Link #"+fmt.Sprintf("%d", i+1)),
				))
		}

		return elem.Div(
			attrs.Props{
				"class": "px-6 pt-4 pb-2",
			},
			elem.P(
				attrs.Props{
					"class": "mb-4 text-base",
				},
				nodes...,
			),
			elem.If(
				currentlyInstalling,
				elem.Node( // If currently installing, show progress bar
					elem.Raw(StartProgressBar(installing.Get(galleryID), "0")),
				), // Otherwise, show install button (if not installed) or display "Installed"
				elem.If(m.Installed,
					span("Installed"),
					installButton(m),
				),
			),
		)
	}

	for _, m := range models {

		elems := []elem.Node{}

		if m.Icon == "" {
			m.Icon = NoImage
		}

		elems = append(elems,

			elem.Div(attrs.Props{
				"class": "flex justify-center items-center",
			},
				elem.A(attrs.Props{
					"href": "#!",
					//		"class": "justify-center items-center",
				},
					elem.Img(attrs.Props{
						//	"class": "rounded-t-lg object-fit object-center h-96",
						"class": "rounded-t-lg max-h-48 max-w-96 object-cover mt-3",
						"src":   m.Icon,
					}),
				),
			))

		elems = append(elems, descriptionDiv(m), actionDiv(m))
		modelsElements = append(modelsElements,
			elem.Div(
				attrs.Props{
					"class": " me-4 mb-2 block rounded-lg bg-white shadow-secondary-1  dark:bg-gray-800 dark:bg-surface-dark dark:text-white text-surface pb-2",
				},
				elem.Div(
					attrs.Props{
						//	"class": "p-6",
					},
					elems...,
				),
			),
		)
	}

	wrapper := elem.Div(attrs.Props{
		"class": "dark grid grid-cols-1 grid-rows-1 md:grid-cols-3 block rounded-lg shadow-secondary-1 dark:bg-surface-dark",
		//"class": "block rounded-lg bg-white shadow-secondary-1 dark:bg-surface-dark",
	}, modelsElements...)

	return wrapper.Render()
}
