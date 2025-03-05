package elements

import (
	"fmt"
	"time"

	"github.com/chasefleming/elem-go"
	"github.com/chasefleming/elem-go/attrs"
	"github.com/microcosm-cc/bluemonday"
	"github.com/mudler/LocalAI/core/p2p"
)

func renderElements(n []elem.Node) string {
	render := ""
	for _, r := range n {
		render += r.Render()
	}
	return render
}

func P2PNodeStats(nodes []p2p.NodeData) string {
	online := 0
	for _, n := range nodes {
		if n.IsOnline() {
			online++
		}
	}

	class := "text-blue-400"
	if online == 0 {
		class = "text-red-400"
	}

	nodesElements := []elem.Node{
		elem.Span(
			attrs.Props{
				"class": class + " font-bold text-xl",
			},
			elem.Text(fmt.Sprintf("%d", online)),
		),
		elem.Span(
			attrs.Props{
				"class": "text-gray-300 text-xl",
			},
			elem.Text(fmt.Sprintf("/%d", len(nodes))),
		),
	}

	return renderElements(nodesElements)
}

func P2PNodeBoxes(nodes []p2p.NodeData) string {
	nodesElements := []elem.Node{}

	for _, n := range nodes {
		nodeID := bluemonday.StrictPolicy().Sanitize(n.ID)

		// Define status-specific classes
		statusIconClass := "text-green-400"
		statusText := "Online"
		statusTextClass := "text-green-400"

		if !n.IsOnline() {
			statusIconClass = "text-red-400"
			statusText = "Offline"
			statusTextClass = "text-red-400"
		}

		nodesElements = append(nodesElements,
			elem.Div(
				attrs.Props{
					"class": "bg-gray-800/80 border border-gray-700/50 rounded-xl p-4 shadow-lg transition-all duration-300 hover:shadow-blue-900/20 hover:border-blue-700/50",
				},
				// Node ID and status indicator in top row
				elem.Div(
					attrs.Props{
						"class": "flex items-center justify-between mb-3",
					},
					// Node ID with icon
					elem.Div(
						attrs.Props{
							"class": "flex items-center",
						},
						elem.I(
							attrs.Props{
								"class": "fas fa-server text-blue-400 mr-2",
							},
						),
						elem.Span(
							attrs.Props{
								"class": "text-white font-medium",
							},
							elem.Text(nodeID),
						),
					),
					// Status indicator
					elem.Div(
						attrs.Props{
							"class": "flex items-center",
						},
						elem.I(
							attrs.Props{
								"class": "fas fa-circle animate-pulse " + statusIconClass + " mr-1.5",
							},
						),
						elem.Span(
							attrs.Props{
								"class": statusTextClass,
							},
							elem.Text(statusText),
						),
					),
				),
				// Bottom section with timestamp
				elem.Div(
					attrs.Props{
						"class": "text-xs text-gray-400 pt-1 border-t border-gray-700/30",
					},
					elem.Text("Last updated: "+time.Now().UTC().Format("2006-01-02 15:04:05")),
				),
			))
	}

	return renderElements(nodesElements)
}
