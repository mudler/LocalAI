package elements

import (
	"strings"

	"github.com/chasefleming/elem-go"
	"github.com/chasefleming/elem-go/attrs"
	"github.com/mudler/LocalAI/core/gallery"
)

func installButton(galleryName string) elem.Node {
	return elem.Button(
		attrs.Props{
			"data-twe-ripple-init":  "",
			"data-twe-ripple-color": "light",
			"class":                 "float-right inline-flex items-center rounded-lg bg-blue-600 hover:bg-blue-700 px-4 py-2 text-sm font-medium text-white transition duration-300 ease-in-out shadow hover:shadow-lg",
			"hx-swap":               "outerHTML",
			// post the Model ID as param
			"hx-post": "browse/install/model/" + galleryName,
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
			"hx-post": "browse/install/model/" + galleryName,
		},
		elem.I(
			attrs.Props{
				"class": "fa-solid fa-arrow-rotate-right pr-2",
			},
		),
		elem.Text("Reinstall"),
	)
}

func infoButton(m *gallery.GalleryModel) elem.Node {
	return elem.Button(
		attrs.Props{
			"data-twe-ripple-init":  "",
			"data-twe-ripple-color": "light",
			"class":                 "inline-flex items-center rounded-lg bg-gray-700 hover:bg-gray-600 px-4 py-2 text-sm font-medium text-white transition duration-300 ease-in-out",
			"data-modal-target":     modalName(m),
			"data-modal-toggle":     modalName(m),
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

func getConfigButton(galleryName string) elem.Node {
	return elem.Button(
		attrs.Props{
			"data-twe-ripple-init":  "",
			"data-twe-ripple-color": "light",
			"class":                 "float-right ml-2 inline-flex items-center rounded-lg bg-gray-700 hover:bg-gray-600 px-4 py-2 text-sm font-medium text-white transition duration-300 ease-in-out",
			"hx-swap":               "outerHTML",
			"hx-post":               "browse/config/model/" + galleryName,
		},
		elem.I(
			attrs.Props{
				"class": "fa-solid fa-download pr-2",
			},
		),
		elem.Text("Get Config"),
	)
}

func deleteButton(galleryID string) elem.Node {
	return elem.Button(
		attrs.Props{
			"data-twe-ripple-init":  "",
			"data-twe-ripple-color": "light",
			"hx-confirm":            "Are you sure you wish to delete the model?",
			"class":                 "float-right inline-block rounded bg-red-800 px-6 pb-2.5 mb-3 pt-2.5 text-xs font-medium uppercase leading-normal text-white shadow-primary-3 transition duration-150 ease-in-out hover:bg-red-accent-300 hover:shadow-red-2 focus:bg-red-accent-300 focus:shadow-primary-2 focus:outline-none focus:ring-0 active:bg-red-600 active:shadow-primary-2 dark:shadow-black/30 dark:hover:shadow-dark-strong dark:focus:shadow-dark-strong dark:active:shadow-dark-strong",
			"hx-target":             "#action-div-" + dropBadChars(galleryID),
			"hx-swap":               "outerHTML",
			// post the Model ID as param
			"hx-post": "browse/delete/model/" + galleryID,
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
