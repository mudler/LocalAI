package elements

import (
	"fmt"

	"github.com/chasefleming/elem-go"
	"github.com/chasefleming/elem-go/attrs"
	"github.com/microcosm-cc/bluemonday"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services"
)

const (
	noImage = "https://upload.wikimedia.org/wikipedia/commons/6/65/No-Image-Placeholder.svg"
)

func cardSpan(text, icon string) elem.Node {
	return elem.Span(
		attrs.Props{
			"class": "inline-block bg-gray-200 rounded-full px-3 py-1 text-sm font-semibold text-gray-700 mr-2 mb-2",
		},
		elem.I(attrs.Props{
			"class": icon + " pr-2",
		}),

		elem.Text(bluemonday.StrictPolicy().Sanitize(text)),
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
				elem.Text(bluemonday.StrictPolicy().Sanitize(text)),
			),
		),
	)
}

/*
func buttonLink(text, url string) elem.Node {
	return elem.A(
		attrs.Props{
			"class":  "inline-block bg-gray-200 rounded-full px-3 py-1 text-sm font-semibold text-gray-700 mr-2 mb-2 hover:bg-gray-300 hover:shadow-gray-2",
			"href":   url,
			"target": "_blank",
		},
		elem.I(attrs.Props{
			"class": "fas fa-link pr-2",
		}),
		elem.Text(bluemonday.StrictPolicy().Sanitize(text)),
	)
}
*/

func link(text, url string) elem.Node {
	return elem.A(
		attrs.Props{
			"class":  "text-base leading-relaxed text-gray-500 dark:text-gray-400",
			"href":   url,
			"target": "_blank",
		},
		elem.I(attrs.Props{
			"class": "fas fa-link pr-2",
		}),
		elem.Text(bluemonday.StrictPolicy().Sanitize(text)),
	)
}

type ProcessTracker interface {
	Exists(string) bool
	Get(string) string
}

func modalName(m *gallery.GalleryModel) string {
	return m.Name + "-modal"
}

func modelDescription(m *gallery.GalleryModel) elem.Node {
	urls := []elem.Node{}
	for _, url := range m.URLs {
		urls = append(urls,
			elem.Li(attrs.Props{}, link(url, url)),
		)
	}

	tagsNodes := []elem.Node{}
	for _, tag := range m.Tags {
		tagsNodes = append(tagsNodes,
			searchableElement(tag, "fas fa-tag"),
		)
	}

	return elem.Div(
		attrs.Props{
			"class": "p-6 text-surface dark:text-white",
		},
		elem.H5(
			attrs.Props{
				"class": "mb-2 text-xl font-bold leading-tight",
			},
			elem.Text(bluemonday.StrictPolicy().Sanitize(m.Name)),
		),
		elem.Div( // small description
			attrs.Props{
				"class": "mb-4 text-sm truncate text-base",
			},
			elem.Text(bluemonday.StrictPolicy().Sanitize(m.Description)),
		),

		elem.Div(
			attrs.Props{
				"id":          modalName(m),
				"tabindex":    "-1",
				"aria-hidden": "true",
				"class":       "hidden overflow-y-auto overflow-x-hidden fixed top-0 right-0 left-0 z-50 justify-center items-center w-full md:inset-0 h-[calc(100%-1rem)] max-h-full",
			},
			elem.Div(
				attrs.Props{
					"class": "relative p-4 w-full max-w-2xl max-h-full",
				},
				elem.Div(
					attrs.Props{
						"class": "relative p-4 w-full max-w-2xl max-h-full bg-white rounded-lg shadow dark:bg-gray-700",
					},
					// header
					elem.Div(
						attrs.Props{
							"class": "flex items-center justify-between p-4 md:p-5 border-b rounded-t dark:border-gray-600",
						},
						elem.H3(
							attrs.Props{
								"class": "text-xl font-semibold text-gray-900 dark:text-white",
							},
							elem.Text(bluemonday.StrictPolicy().Sanitize(m.Name)),
						),
						elem.Button( // close button
							attrs.Props{
								"class":           "text-gray-400 bg-transparent hover:bg-gray-200 hover:text-gray-900 rounded-lg text-sm w-8 h-8 ms-auto inline-flex justify-center items-center dark:hover:bg-gray-600 dark:hover:text-white",
								"data-modal-hide": modalName(m),
							},
							elem.Raw(
								`<svg class="w-3 h-3" aria-hidden="true" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 14 14">
									<path stroke="currentColor" stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="m1 1 6 6m0 0 6 6M7 7l6-6M7 7l-6 6"/>
								</svg>`,
							),
							elem.Span(
								attrs.Props{
									"class": "sr-only",
								},
								elem.Text("Close modal"),
							),
						),
					),
					// body
					elem.Div(
						attrs.Props{
							"class": "p-4 md:p-5 space-y-4",
						},
						elem.Div(
							attrs.Props{
								"class": "flex justify-center items-center",
							},
							elem.Img(attrs.Props{
								//	"class": "rounded-t-lg object-fit object-center h-96",
								"class":   "lazy rounded-t-lg max-h-48 max-w-96 object-cover mt-3 entered loaded",
								"src":     m.Icon,
								"loading": "lazy",
							}),
						),
						elem.P(
							attrs.Props{
								"class": "text-base leading-relaxed text-gray-500 dark:text-gray-400",
							},
							elem.Text(bluemonday.StrictPolicy().Sanitize(m.Description)),
						),
						elem.Hr(
							attrs.Props{},
						),
						elem.P(
							attrs.Props{
								"class": "text-sm font-semibold text-gray-900 dark:text-white",
							},
							elem.Text("Links"),
						),
						elem.Ul(
							attrs.Props{},
							urls...,
						),
						elem.If(
							len(m.Tags) > 0,
							elem.Div(
								attrs.Props{},
								elem.P(
									attrs.Props{
										"class": "text-sm mb-5 font-semibold text-gray-900 dark:text-white",
									},
									elem.Text("Tags"),
								),
								elem.Div(
									attrs.Props{
										"class": "flex flex-row flex-wrap content-center",
									},
									tagsNodes...,
								),
							),
							elem.Div(attrs.Props{}),
						),
					),
					// Footer
					elem.Div(
						attrs.Props{
							"class": "flex items-center p-4 md:p-5 border-t border-gray-200 rounded-b dark:border-gray-600",
						},
						elem.Button(
							attrs.Props{
								"data-modal-hide": modalName(m),
								"class":           "py-2.5 px-5 ms-3 text-sm font-medium text-gray-900 focus:outline-none bg-white rounded-lg border border-gray-200 hover:bg-gray-100 hover:text-blue-700 focus:z-10 focus:ring-4 focus:ring-gray-100 dark:focus:ring-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:border-gray-600 dark:hover:text-white dark:hover:bg-gray-700",
							},
							elem.Text("Close"),
						),
					),
				),
			),
		),
	)
}

func modelActionItems(m *gallery.GalleryModel, processTracker ProcessTracker, galleryService *services.GalleryService) elem.Node {
	galleryID := fmt.Sprintf("%s@%s", m.Gallery.Name, m.Name)
	currentlyProcessing := processTracker.Exists(galleryID)
	jobID := ""
	isDeletionOp := false
	if currentlyProcessing {
		status := galleryService.GetStatus(galleryID)
		if status != nil && status.Deletion {
			isDeletionOp = true
		}
		jobID = processTracker.Get(galleryID)
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
	/*
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
						buttonLink("Link #"+fmt.Sprintf("%d", i+1), url),
					)
				}
	*/

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
				"id":    "action-div-" + dropBadChars(galleryID),
				"class": "flow-root", // To order buttons left and right
			},
			infoButton(m),
			elem.Div(
				attrs.Props{
					"class": "float-right",
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
							deleteButton(m.ID()),
						)),
						installButton(m.ID()),
					),
				),
			),
		),
	)
}

func ListModels(models []*gallery.GalleryModel, processTracker ProcessTracker, galleryService *services.GalleryService) string {
	modelsElements := []elem.Node{}

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
						"class":   "rounded-t-lg max-h-48 max-w-96 object-cover mt-3",
						"src":     m.Icon,
						"loading": "lazy",
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

		elems = append(elems,
			modelDescription(m),
			modelActionItems(m, processTracker, galleryService),
		)
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
