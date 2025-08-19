package elements

import (
	"fmt"
	"time"

	"github.com/chasefleming/elem-go"
	"github.com/chasefleming/elem-go/attrs"
	"github.com/microcosm-cc/bluemonday"
	"github.com/mudler/LocalAI/core/schema"
)

func renderElements(n []elem.Node) string {
	render := ""
	for _, r := range n {
		render += r.Render()
	}
	return render
}

func P2PNodeStats(nodes []schema.NodeData) string {
	online := 0
	for _, n := range nodes {
		if n.IsOnline() {
			online++
		}
	}

	class := "text-green-400"
	if online == 0 {
		class = "text-red-400"
	} else if online < len(nodes) {
		class = "text-yellow-400"
	}

	nodesElements := []elem.Node{
		elem.Span(
			attrs.Props{
				"class": class + " font-bold text-2xl",
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

func P2PNodeBoxes(nodes []schema.NodeData) string {
	if len(nodes) == 0 {
		return `<div class="col-span-full flex flex-col items-center justify-center py-12 text-center bg-gray-800/50 border border-gray-700/50 rounded-xl">
			<i class="fas fa-server text-gray-500 text-4xl mb-4"></i>
			<p class="text-gray-400 text-lg font-medium">No nodes available</p>
			<p class="text-gray-500 text-sm mt-2">Start some workers to see them here</p>
		</div>`
	}

	render := ""
	for _, n := range nodes {
		nodeID := bluemonday.StrictPolicy().Sanitize(n.ID)

		// Define status-specific classes
		statusIconClass := "text-green-400"
		statusText := "Online"
		statusTextClass := "text-green-400"
		cardHoverClass := "hover:shadow-green-500/20 hover:border-green-400/50"

		if !n.IsOnline() {
			statusIconClass = "text-red-400"
			statusText = "Offline"
			statusTextClass = "text-red-400"
			cardHoverClass = "hover:shadow-red-500/20 hover:border-red-400/50"
		}

		nodeCard := elem.Div(
			attrs.Props{
				"class": "bg-gradient-to-br from-gray-800/90 to-gray-900/80 border border-gray-700/50 rounded-xl p-5 shadow-xl transition-all duration-300 " + cardHoverClass + " backdrop-blur-sm",
			},
			// Header with node icon and status
			elem.Div(
				attrs.Props{
					"class": "flex items-center justify-between mb-4",
				},
				// Node info
				elem.Div(
					attrs.Props{
						"class": "flex items-center",
					},
					elem.Div(
						attrs.Props{
							"class": "w-10 h-10 bg-blue-500/20 rounded-lg flex items-center justify-center mr-3",
						},
						elem.I(
							attrs.Props{
								"class": "fas fa-server text-blue-400 text-lg",
							},
						),
					),
					elem.Div(
						attrs.Props{},
						elem.H4(
							attrs.Props{
								"class": "text-white font-semibold text-sm",
							},
							elem.Text("Node"),
						),
						elem.P(
							attrs.Props{
								"class": "text-gray-400 text-xs font-mono break-all",
							},
							elem.Text(nodeID),
						),
					),
				),
				// Status badge
				elem.Div(
					attrs.Props{
						"class": "flex items-center bg-gray-900/50 rounded-full px-3 py-1.5 border border-gray-700/50",
					},
					elem.I(
						attrs.Props{
							"class": "fas fa-circle animate-pulse " + statusIconClass + " mr-2 text-xs",
						},
					),
					elem.Span(
						attrs.Props{
							"class": statusTextClass + " text-xs font-medium",
						},
						elem.Text(statusText),
					),
				),
			),
			// Footer with timestamp
			elem.Div(
				attrs.Props{
					"class": "text-xs text-gray-500 pt-3 border-t border-gray-700/30 flex items-center",
				},
				elem.I(
					attrs.Props{
						"class": "fas fa-clock mr-2",
					},
				),
				elem.Text("Updated: "+time.Now().UTC().Format("15:04:05")),
			),
		)

		render += nodeCard.Render()
	}

	return render
}
