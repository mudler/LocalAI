package elements

import (
	"github.com/chasefleming/elem-go"
	"github.com/chasefleming/elem-go/attrs"
	"github.com/microcosm-cc/bluemonday"
)

func DoneProgress(galleryID, text string, showDelete bool) string {
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
			elem.Text(bluemonday.StrictPolicy().Sanitize(text)),
		),
		elem.If(showDelete, deleteButton(galleryID), reInstallButton(galleryID)),
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
			elem.Text("Error "+bluemonday.StrictPolicy().Sanitize(err)),
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
			elem.Text(bluemonday.StrictPolicy().Sanitize(text)), //Perhaps overly defensive
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
