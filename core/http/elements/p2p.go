package elements

import (
	"fmt"

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
	/*
	   <div class="bg-gray-800 p-6 rounded-lg shadow-lg text-left">
	                       <p class="text-xl font-semibold text-gray-200">Total Workers Detected: {{ len .Nodes }}</p>
	                       {{ $online := 0 }}
	                       {{ range .Nodes }}
	                           {{ if .IsOnline }}
	                               {{ $online = add $online 1 }}
	                           {{ end }}
	                       {{ end }}
	                       <p class="text-xl font-semibold text-gray-200">Total Online Workers: {{$online}}</p>
	                   </div>
	*/

	online := 0
	for _, n := range nodes {
		if n.IsOnline() {
			online++
		}
	}

	class := "text-green-500"
	if online == 0 {
		class = "text-red-500"
	}
	/*
	   <i class="fas fa-circle animate-pulse text-green-500 ml-2 mr-1"></i>
	*/
	circle := elem.I(attrs.Props{
		"class": "fas fa-circle animate-pulse " + class + " ml-2 mr-1",
	})
	nodesElements := []elem.Node{
		elem.Span(
			attrs.Props{
				"class": class,
			},
			circle,
			elem.Text(fmt.Sprintf("%d", online)),
		),
		elem.Span(
			attrs.Props{
				"class": "text-gray-200",
			},
			elem.Text(fmt.Sprintf("/%d", len(nodes))),
		),
	}

	return renderElements(nodesElements)
}

func P2PNodeBoxes(nodes []p2p.NodeData) string {
	/*
			<div class="bg-gray-800 p-4 rounded-lg shadow-lg text-left">
			<div class="flex items-center mb-2">
				<i class="fas fa-desktop text-gray-400 mr-2"></i>
				<span class="text-gray-200 font-semibold">{{.ID}}</span>
			</div>
			<p class="text-sm text-gray-400 mt-2 flex items-center">
				Status:
				<i class="fas fa-circle {{ if .IsOnline }}text-green-500{{ else }}text-red-500{{ end }} ml-2 mr-1"></i>
				<span class="{{ if .IsOnline }}text-green-400{{ else }}text-red-400{{ end }}">
					{{ if .IsOnline }}Online{{ else }}Offline{{ end }}
				</span>
			</p>
		</div>
	*/

	nodesElements := []elem.Node{}

	for _, n := range nodes {

		nodesElements = append(nodesElements,
			elem.Div(
				attrs.Props{
					"class": "bg-gray-700 p-6 rounded-lg shadow-lg text-left",
				},
				elem.P(
					attrs.Props{
						"class": "text-sm text-gray-400 mt-2 flex",
					},
					elem.I(
						attrs.Props{
							"class": "fas fa-desktop text-gray-400 mr-2",
						},
					),
					elem.Text("Name: "),
					elem.Span(
						attrs.Props{
							"class": "text-gray-200 font-semibold ml-2 mr-1",
						},
						elem.Text(bluemonday.StrictPolicy().Sanitize(n.ID)),
					),
					elem.Text("Status: "),
					elem.If(
						n.IsOnline(),
						elem.I(
							attrs.Props{
								"class": "fas fa-circle animate-pulse text-green-500 ml-2 mr-1",
							},
						),
						elem.I(
							attrs.Props{
								"class": "fas fa-circle animate-pulse text-red-500 ml-2 mr-1",
							},
						),
					),
					elem.If(
						n.IsOnline(),
						elem.Span(
							attrs.Props{
								"class": "text-green-400",
							},

							elem.Text("Online"),
						),
						elem.Span(
							attrs.Props{
								"class": "text-red-400",
							},
							elem.Text("Offline"),
						),
					),
				),
			))
	}

	return renderElements(nodesElements)
}
