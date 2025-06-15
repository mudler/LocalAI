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
			"class": "inline-flex items-center px-3 py-1 rounded-lg text-xs font-medium bg-gray-700/70 text-gray-300 border border-gray-600/50 mr-2 mb-2",
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
				"class": "inline-flex items-center text-xs px-3 py-1 rounded-full bg-gray-700/60 text-gray-300 border border-gray-600/50 hover:bg-gray-600 hover:text-gray-100 transition duration-200 ease-in-out",
			},
			elem.A(
				attrs.Props{
					//	"name":      "search",
					//	"value":     text,
					//"class":     "inline-block bg-gray-200 rounded-full px-3 py-1 text-sm font-semibold text-gray-700 mr-2 mb-2",
					//"href":      "#!",
					"href": "browse?term=" + text,
					//"hx-post":   "browse/search/models",
					//"hx-target": "#search-results",
					// TODO: this doesn't work
					//	"hx-vals":      `{ \"search\": \"` + text + `\" }`,
					//"hx-indicator": ".htmx-indicator",
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

func modelModal(m *gallery.GalleryModel) elem.Node {
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
			"id":          modalName(m),
			"tabindex":    "-1",
			"aria-hidden": "true",
			"class":       "hidden fixed top-0 right-0 left-0 z-50 justify-center items-center w-full md:inset-0 h-full max-h-full bg-gray-900/50",
		},
		elem.Div(
			attrs.Props{
				"class": "relative p-4 w-full max-w-2xl h-[90vh] mx-auto mt-[5vh]",
			},
			elem.Div(
				attrs.Props{
					"class": "relative bg-white rounded-lg shadow dark:bg-gray-700 h-full flex flex-col",
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
						"class": "p-4 md:p-5 space-y-4 overflow-y-auto flex-1 min-h-0",
					},
					elem.Div(
						attrs.Props{
							"class": "flex justify-center items-center",
						},
						elem.Img(attrs.Props{
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
	)
}

func modelDescription(m *gallery.GalleryModel) elem.Node {
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
						elem.Raw(StartModelProgressBar(jobID, "0", progressMessage)),
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
					"class": " me-4 mb-2 block rounded-lg bg-white shadow-secondary-1  dark:bg-gray-800 dark:bg-surface-dark dark:text-white text-surface pb-2 bg-gray-800/90 border border-gray-700/50 rounded-xl overflow-hidden transition-all duration-300 hover:shadow-lg hover:shadow-blue-900/20 hover:-translate-y-1 hover:border-blue-700/50",
				},
				elem.Div(
					attrs.Props{
						//	"class": "p-6",
					},
					elems...,
				),
			),
			modelModal(m),
		)
	}

	wrapper := elem.Div(attrs.Props{
		"class": "dark grid grid-cols-1 grid-rows-1 md:grid-cols-3 block rounded-lg shadow-secondary-1 dark:bg-surface-dark",
	}, modelsElements...)

	return wrapper.Render()
}

func ListBackends(backends []*gallery.GalleryBackend, processTracker ProcessTracker, galleryService *services.GalleryService) string {
	backendsElements := []elem.Node{}

	for _, b := range backends {
		elems := []elem.Node{}

		if b.Icon == "" {
			b.Icon = noImage
		}

		divProperties := attrs.Props{
			"class": "flex justify-center items-center",
		}

		elems = append(elems,
			elem.Div(divProperties,
				elem.A(attrs.Props{
					"href": "#!",
				},
					elem.Img(attrs.Props{
						"class":   "rounded-t-lg max-h-48 max-w-96 object-cover mt-3",
						"src":     b.Icon,
						"loading": "lazy",
					}),
				),
			),
		)

		elems = append(elems,
			backendDescription(b),
			backendActionItems(b, processTracker, galleryService),
		)
		backendsElements = append(backendsElements,
			elem.Div(
				attrs.Props{
					"class": "me-4 mb-2 block rounded-lg bg-white shadow-secondary-1 dark:bg-gray-800 dark:bg-surface-dark dark:text-white text-surface pb-2 bg-gray-800/90 border border-gray-700/50 rounded-xl overflow-hidden transition-all duration-300 hover:shadow-lg hover:shadow-blue-900/20 hover:-translate-y-1 hover:border-blue-700/50",
				},
				elem.Div(
					attrs.Props{},
					elems...,
				),
			),
			backendModal(b),
		)
	}

	wrapper := elem.Div(attrs.Props{
		"class": "dark grid grid-cols-1 grid-rows-1 md:grid-cols-3 block rounded-lg shadow-secondary-1 dark:bg-surface-dark",
	}, backendsElements...)

	return wrapper.Render()
}

func backendDescription(b *gallery.GalleryBackend) elem.Node {
	return elem.Div(
		attrs.Props{
			"class": "p-6 text-surface dark:text-white",
		},
		elem.H5(
			attrs.Props{
				"class": "mb-2 text-xl font-bold leading-tight",
			},
			elem.Text(bluemonday.StrictPolicy().Sanitize(b.Name)),
		),
		elem.Div(
			attrs.Props{
				"class": "mb-4 text-sm truncate text-base",
			},
			elem.Text(bluemonday.StrictPolicy().Sanitize(b.Description)),
		),
	)
}

func backendActionItems(b *gallery.GalleryBackend, processTracker ProcessTracker, galleryService *services.GalleryService) elem.Node {
	galleryID := fmt.Sprintf("%s@%s", b.Gallery.Name, b.Name)
	currentlyProcessing := processTracker.Exists(galleryID)
	jobID := ""
	isDeletionOp := false
	if currentlyProcessing {
		status := galleryService.GetStatus(galleryID)
		if status != nil && status.Deletion {
			isDeletionOp = true
		}
		jobID = processTracker.Get(galleryID)
	}

	nodes := []elem.Node{
		cardSpan("Repository: "+b.Gallery.Name, "fa-brands fa-git-alt"),
	}

	if b.License != "" {
		nodes = append(nodes,
			cardSpan("License: "+b.License, "fas fa-book"),
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
				"id":    "action-div-" + dropBadChars(galleryID),
				"class": "flow-root",
			},
			backendInfoButton(b),
			elem.Div(
				attrs.Props{
					"class": "float-right",
				},
				elem.If(
					currentlyProcessing,
					elem.Node(
						elem.Raw(StartModelProgressBar(jobID, "0", progressMessage)),
					),
					elem.If(b.Installed,
						elem.Node(elem.Div(
							attrs.Props{},
							backendReInstallButton(galleryID),
							backendDeleteButton(galleryID),
						)),
						backendInstallButton(galleryID),
					),
				),
			),
		),
	)
}

func backendModal(b *gallery.GalleryBackend) elem.Node {
	urls := []elem.Node{}
	for _, url := range b.URLs {
		urls = append(urls,
			elem.Li(attrs.Props{}, link(url, url)),
		)
	}

	tagsNodes := []elem.Node{}
	for _, tag := range b.Tags {
		tagsNodes = append(tagsNodes,
			searchableElement(tag, "fas fa-tag"),
		)
	}

	modalID := fmt.Sprintf("modal-%s", dropBadChars(fmt.Sprintf("%s@%s", b.Gallery.Name, b.Name)))

	return elem.Div(
		attrs.Props{
			"id":          modalID,
			"tabindex":    "-1",
			"aria-hidden": "true",
			"class":       "hidden fixed top-0 right-0 left-0 z-50 justify-center items-center w-full md:inset-0 h-full max-h-full bg-gray-900/50",
		},
		elem.Div(
			attrs.Props{
				"class": "relative p-4 w-full max-w-2xl h-[90vh] mx-auto mt-[5vh]",
			},
			elem.Div(
				attrs.Props{
					"class": "relative bg-white rounded-lg shadow dark:bg-gray-700 h-full flex flex-col",
				},
				elem.Div(
					attrs.Props{
						"class": "flex items-center justify-between p-4 md:p-5 border-b rounded-t dark:border-gray-600",
					},
					elem.H3(
						attrs.Props{
							"class": "text-xl font-semibold text-gray-900 dark:text-white",
						},
						elem.Text(bluemonday.StrictPolicy().Sanitize(b.Name)),
					),
					elem.Button(
						attrs.Props{
							"class":           "text-gray-400 bg-transparent hover:bg-gray-200 hover:text-gray-900 rounded-lg text-sm w-8 h-8 ms-auto inline-flex justify-center items-center dark:hover:bg-gray-600 dark:hover:text-white",
							"data-modal-hide": modalID,
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
				elem.Div(
					attrs.Props{
						"class": "p-4 md:p-5 space-y-4 overflow-y-auto flex-grow",
					},
					elem.Div(
						attrs.Props{
							"class": "flex justify-center items-center",
						},
						elem.Img(attrs.Props{
							"src":     b.Icon,
							"class":   "rounded-t-lg max-h-48 max-w-96 object-cover mt-3",
							"loading": "lazy",
						}),
					),
					elem.P(
						attrs.Props{
							"class": "text-base leading-relaxed text-gray-500 dark:text-gray-400",
						},
						elem.Text(bluemonday.StrictPolicy().Sanitize(b.Description)),
					),
					elem.Div(
						attrs.Props{
							"class": "flex flex-wrap gap-2",
						},
						tagsNodes...,
					),
					elem.Div(
						attrs.Props{
							"class": "text-base leading-relaxed text-gray-500 dark:text-gray-400",
						},
						elem.Ul(attrs.Props{}, urls...),
					),
				),
				elem.Div(
					attrs.Props{
						"class": "flex items-center p-4 md:p-5 border-t border-gray-200 rounded-b dark:border-gray-600",
					},
					elem.Button(
						attrs.Props{
							"data-modal-hide": modalID,
							"type":            "button",
							"class":           "text-white bg-blue-700 hover:bg-blue-800 focus:ring-4 focus:outline-none focus:ring-blue-300 font-medium rounded-lg text-sm px-5 py-2.5 text-center dark:bg-blue-600 dark:hover:bg-blue-700 dark:focus:ring-blue-800",
						},
						elem.Text("Close"),
					),
				),
			),
		),
	)
}

func backendInfoButton(b *gallery.GalleryBackend) elem.Node {
	modalID := fmt.Sprintf("modal-%s", dropBadChars(fmt.Sprintf("%s@%s", b.Gallery.Name, b.Name)))
	return elem.Button(
		attrs.Props{
			"data-twe-ripple-init":  "",
			"data-twe-ripple-color": "light",
			"class":                 "inline-flex items-center rounded-lg bg-gray-700 hover:bg-gray-600 px-4 py-2 text-sm font-medium text-white transition duration-300 ease-in-out",
			"data-modal-target":     modalID,
			"data-modal-toggle":     modalID,
		},
		elem.P(
			attrs.Props{
				"class": "flex items-center",
			},
			elem.I(
				attrs.Props{
					"class": "fas fa-info-circle pr-2",
				},
			),
			elem.Text("Info"),
		),
	)
}

func backendInstallButton(galleryID string) elem.Node {
	return elem.Button(
		attrs.Props{
			"data-twe-ripple-init":  "",
			"data-twe-ripple-color": "light",
			"class":                 "float-right inline-flex items-center rounded-lg bg-blue-600 hover:bg-blue-700 px-4 py-2 text-sm font-medium text-white transition duration-300 ease-in-out shadow hover:shadow-lg",
			"hx-swap":               "outerHTML",
			"hx-post":               "browse/install/backend/" + galleryID,
		},
		elem.I(
			attrs.Props{
				"class": "fa-solid fa-download pr-2",
			},
		),
		elem.Text("Install"),
	)
}

func backendReInstallButton(galleryID string) elem.Node {
	return elem.Button(
		attrs.Props{
			"data-twe-ripple-init":  "",
			"data-twe-ripple-color": "light",
			"class":                 "float-right inline-block rounded bg-primary ml-2 px-6 pb-2.5 mb-3 pt-2.5 text-xs font-medium uppercase leading-normal text-white shadow-primary-3 transition duration-150 ease-in-out hover:bg-primary-accent-300 hover:shadow-primary-2 focus:bg-primary-accent-300 focus:shadow-primary-2 focus:outline-none focus:ring-0 active:bg-primary-600 active:shadow-primary-2 dark:shadow-black/30 dark:hover:shadow-dark-strong dark:focus:shadow-dark-strong dark:active:shadow-dark-strong",
			"hx-target":             "#action-div-" + dropBadChars(galleryID),
			"hx-swap":               "outerHTML",
			"hx-post":               "browse/install/backend/" + galleryID,
		},
		elem.I(
			attrs.Props{
				"class": "fa-solid fa-arrow-rotate-right pr-2",
			},
		),
		elem.Text("Reinstall"),
	)
}

func backendDeleteButton(galleryID string) elem.Node {
	return elem.Button(
		attrs.Props{
			"data-twe-ripple-init":  "",
			"data-twe-ripple-color": "light",
			"hx-confirm":            "Are you sure you wish to delete the backend?",
			"class":                 "float-right inline-block rounded bg-red-800 px-6 pb-2.5 mb-3 pt-2.5 text-xs font-medium uppercase leading-normal text-white shadow-primary-3 transition duration-150 ease-in-out hover:bg-red-accent-300 hover:shadow-red-2 focus:bg-red-accent-300 focus:shadow-primary-2 focus:outline-none focus:ring-0 active:bg-red-600 active:shadow-primary-2 dark:shadow-black/30 dark:hover:shadow-dark-strong dark:focus:shadow-dark-strong dark:active:shadow-dark-strong",
			"hx-target":             "#action-div-" + dropBadChars(galleryID),
			"hx-swap":               "outerHTML",
			"hx-post":               "browse/delete/backend/" + galleryID,
		},
		elem.I(
			attrs.Props{
				"class": "fa-solid fa-cancel pr-2",
			},
		),
		elem.Text("Delete"),
	)
}
