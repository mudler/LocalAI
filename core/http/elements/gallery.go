package elements

import (
	"fmt"
	"strings"

	"github.com/chasefleming/elem-go"
	"github.com/chasefleming/elem-go/attrs"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/xsync"
)

const (
	noImage = "https://upload.wikimedia.org/wikipedia/commons/6/65/No-Image-Placeholder.svg"
)

func DoneProgress(galleryID, text string, showDelete bool) string {
	var modelName = galleryID
	// Split by @ and grab the name
	if strings.Contains(galleryID, "@") {
		modelName = strings.Split(galleryID, "@")[1]
	}

	return elem.Div(
		attrs.Props{
			"id": "action-div-" + dropBadChars(galleryID),
		},
		elem.H3(
			attrs.Props{
				"role":      "status",
				"id":        "pblabel",
				"tabindex":  "-1",
				"autofocus": "",
			},
			elem.Text(text),
		),
		elem.If(showDelete, deleteButton(galleryID, modelName), reInstallButton(galleryID)),
	).Render()
}

func ErrorProgress(err, galleryName string) string {
	return elem.Div(
		attrs.Props{},
		elem.H3(
			attrs.Props{
				"role":      "status",
				"id":        "pblabel",
				"tabindex":  "-1",
				"autofocus": "",
			},
			elem.Text("Error "+err),
		),
		installButton(galleryName),
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

func StartProgressBar(uid, progress, text string) string {
	if progress == "" {
		progress = "0"
	}
	return elem.Div(
		attrs.Props{
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
			elem.Text(text),
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

		//elem.Text(text),
	)
}

func searchableElement(text, icon string) elem.Node {
	return elem.Form(
		attrs.Props{},
		elem.Input(
			attrs.Props{
				"type":  "hidden",
				"name":  "search",
				"value": text,
			},
		),
		elem.Span(
			attrs.Props{
				"class": "inline-block bg-gray-200 rounded-full px-3 py-1 text-sm font-semibold text-gray-700 mr-2 mb-2 hover:bg-gray-300 hover:shadow-gray-2",
			},

			elem.A(
				attrs.Props{
					//	"name":      "search",
					//	"value":     text,
					//"class":     "inline-block bg-gray-200 rounded-full px-3 py-1 text-sm font-semibold text-gray-700 mr-2 mb-2",
					"href":      "#!",
					"hx-post":   "/browse/search/models",
					"hx-target": "#search-results",
					// TODO: this doesn't work
					//	"hx-vals":      `{ \"search\": \"` + text + `\" }`,
					"hx-indicator": ".htmx-indicator",
				},
				elem.I(attrs.Props{
					"class": icon + " pr-2",
				}),
				elem.Text(text),
			),
		),

		//elem.Text(text),
	)
}

func link(text, url string) elem.Node {
	return elem.A(
		attrs.Props{
			"class":  "inline-block bg-gray-200 rounded-full px-3 py-1 text-sm font-semibold text-gray-700 mr-2 mb-2 hover:bg-gray-300 hover:shadow-gray-2",
			"href":   url,
			"target": "_blank",
		},
		elem.I(attrs.Props{
			"class": "fas fa-link pr-2",
		}),
		elem.Text(text),
	)
}
func installButton(galleryName string) elem.Node {
	return elem.Button(
		attrs.Props{
			"data-twe-ripple-init":  "",
			"data-twe-ripple-color": "light",
			"class":                 "float-right inline-block rounded bg-primary px-6 pb-2.5 mb-3 pt-2.5 text-xs font-medium uppercase leading-normal text-white shadow-primary-3 transition duration-150 ease-in-out hover:bg-primary-accent-300 hover:shadow-primary-2 focus:bg-primary-accent-300 focus:shadow-primary-2 focus:outline-none focus:ring-0 active:bg-primary-600 active:shadow-primary-2 dark:shadow-black/30 dark:hover:shadow-dark-strong dark:focus:shadow-dark-strong dark:active:shadow-dark-strong",
			"hx-swap":               "outerHTML",
			// post the Model ID as param
			"hx-post": "/browse/install/model/" + galleryName,
		},
		elem.I(
			attrs.Props{
				"class": "fa-solid fa-download pr-2",
			},
		),
		elem.Text("Install"),
	)
}

func reInstallButton(galleryName string) elem.Node {
	return elem.Button(
		attrs.Props{
			"data-twe-ripple-init":  "",
			"data-twe-ripple-color": "light",
			"class":                 "float-right inline-block rounded bg-primary ml-2 px-6 pb-2.5 mb-3 pt-2.5 text-xs font-medium uppercase leading-normal text-white shadow-primary-3 transition duration-150 ease-in-out hover:bg-primary-accent-300 hover:shadow-primary-2 focus:bg-primary-accent-300 focus:shadow-primary-2 focus:outline-none focus:ring-0 active:bg-primary-600 active:shadow-primary-2 dark:shadow-black/30 dark:hover:shadow-dark-strong dark:focus:shadow-dark-strong dark:active:shadow-dark-strong",
			"hx-target":             "#action-div-" + dropBadChars(galleryName),
			"hx-swap":               "outerHTML",
			// post the Model ID as param
			"hx-post": "/browse/install/model/" + galleryName,
		},
		elem.I(
			attrs.Props{
				"class": "fa-solid fa-arrow-rotate-right pr-2",
			},
		),
		elem.Text("Reinstall"),
	)
}

func deleteButton(galleryID, modelName string) elem.Node {
	return elem.Button(
		attrs.Props{
			"data-twe-ripple-init":  "",
			"data-twe-ripple-color": "light",
			"hx-confirm":            "Are you sure you wish to delete the model?",
			"class":                 "float-right inline-block rounded bg-red-800 px-6 pb-2.5 mb-3 pt-2.5 text-xs font-medium uppercase leading-normal text-white shadow-primary-3 transition duration-150 ease-in-out hover:bg-red-accent-300 hover:shadow-red-2 focus:bg-red-accent-300 focus:shadow-primary-2 focus:outline-none focus:ring-0 active:bg-red-600 active:shadow-primary-2 dark:shadow-black/30 dark:hover:shadow-dark-strong dark:focus:shadow-dark-strong dark:active:shadow-dark-strong",
			"hx-target":             "#action-div-" + dropBadChars(galleryID),
			"hx-swap":               "outerHTML",
			// post the Model ID as param
			"hx-post": "/browse/delete/model/" + galleryID,
		},
		elem.I(
			attrs.Props{
				"class": "fa-solid fa-cancel pr-2",
			},
		),
		elem.Text("Delete"),
	)
}

// Javascript/HTMX doesn't like weird IDs
func dropBadChars(s string) string {
	return strings.ReplaceAll(s, "@", "__")
}

func ListModels(models []*gallery.GalleryModel, processing *xsync.SyncedMap[string, string], galleryService *services.GalleryService) string {
	modelsElements := []elem.Node{}
	descriptionDiv := func(m *gallery.GalleryModel) elem.Node {
		return elem.Div(
			attrs.Props{
				"class": "p-6 text-surface dark:text-white",
			},
			elem.H5(
				attrs.Props{
					"class": "mb-2 text-xl font-bold leading-tight",
				},
				elem.Text(m.Name),
			),
			elem.P(
				attrs.Props{
					"class": "mb-4 text-sm [&:not(:hover)]:truncate text-base",
				},
				elem.Text(m.Description),
			),
		)
	}

	actionDiv := func(m *gallery.GalleryModel) elem.Node {
		galleryID := fmt.Sprintf("%s@%s", m.Gallery.Name, m.Name)
		currentlyProcessing := processing.Exists(galleryID)
		jobID := ""
		isDeletionOp := false
		if currentlyProcessing {
			status := galleryService.GetStatus(galleryID)
			if status != nil && status.Deletion {
				isDeletionOp = true
			}
			jobID = processing.Get(galleryID)
			// TODO:
			// case not handled, if status == nil : "Waiting"
		}

		nodes := []elem.Node{
			cardSpan("Repository: "+m.Gallery.Name, "fa-brands fa-git-alt"),
		}

		if m.License != "" {
			nodes = append(nodes,
				cardSpan("License: "+m.License, "fas fa-book"),
			)
		}

		tagsNodes := []elem.Node{}
		for _, tag := range m.Tags {
			tagsNodes = append(tagsNodes,
				searchableElement(tag, "fas fa-tag"),
			)
		}

		nodes = append(nodes,
			elem.Div(
				attrs.Props{
					"class": "flex flex-row flex-wrap content-center",
				},
				tagsNodes...,
			),
		)

		for i, url := range m.URLs {
			nodes = append(nodes,
				link("Link #"+fmt.Sprintf("%d", i+1), url),
			)
		}

		progressMessage := "Installation"
		if isDeletionOp {
			progressMessage = "Deletion"
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
			elem.Div(
				attrs.Props{
					"id": "action-div-" + dropBadChars(galleryID),
				},
				elem.If(
					currentlyProcessing,
					elem.Node( // If currently installing, show progress bar
						elem.Raw(StartProgressBar(jobID, "0", progressMessage)),
					), // Otherwise, show install button (if not installed) or display "Installed"
					elem.If(m.Installed,
						elem.Node(elem.Div(
							attrs.Props{},
							reInstallButton(m.ID()),
							deleteButton(m.ID(), m.Name),
						)),
						installButton(m.ID()),
					),
				),
			),
		)
	}

	for _, m := range models {
		elems := []elem.Node{}

		if m.Icon == "" {
			m.Icon = noImage
		}

		divProperties := attrs.Props{
			"class": "flex justify-center items-center",
		}

		elems = append(elems,
			elem.Div(divProperties,
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
			),
		)

		// Special/corner case: if a model sets Trust Remote Code as required, show a warning
		// TODO: handle this more generically later
		_, trustRemoteCodeExists := m.Overrides["trust_remote_code"]
		if trustRemoteCodeExists {
			elems = append(elems, elem.Div(
				attrs.Props{
					"class": "flex justify-center items-center bg-red-500 text-white p-2 rounded-lg mt-2",
				},
				elem.I(attrs.Props{
					"class": "fa-solid fa-circle-exclamation pr-2",
				}),
				elem.Text("Attention: Trust Remote Code is required for this model"),
			))
		}

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
	}, modelsElements...)

	return wrapper.Render()
}
