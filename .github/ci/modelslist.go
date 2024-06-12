package main

import (
	"fmt"
	"html/template"
	"io/ioutil"
	"os"

	"gopkg.in/yaml.v3"
)

var modelPageTemplate string = `
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>LocalAI models</title>
    <link href="https://cdnjs.cloudflare.com/ajax/libs/flowbite/2.3.0/flowbite.min.css" rel="stylesheet" />
    <script src="https://cdn.jsdelivr.net/npm/vanilla-lazyload@19.1.3/dist/lazyload.min.js"></script>

    <link
    rel="stylesheet"
    href="https://cdn.jsdelivr.net/gh/highlightjs/cdn-release@11.8.0/build/styles/default.min.css"
  />
    <script
    defer
    src="https://cdn.jsdelivr.net/gh/highlightjs/cdn-release@11.8.0/build/highlight.min.js"
  ></script>
    <script
    defer
    src="https://cdn.jsdelivr.net/npm/alpinejs@3.x.x/dist/cdn.min.js"
  ></script>
  <script
    defer
    src="https://cdn.jsdelivr.net/npm/marked/marked.min.js"
  ></script>
  <script
    defer
    src="https://cdn.jsdelivr.net/npm/dompurify@3.0.6/dist/purify.min.js"
  ></script>

  <link href="/static/general.css" rel="stylesheet" />
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;600;700&family=Roboto:wght@400;500&display=swap" rel="stylesheet">
    <link
    href="https://fonts.googleapis.com/css?family=Roboto:300,400,500,700,900&display=swap"
    rel="stylesheet" />
  <link
    rel="stylesheet"
    href="https://cdn.jsdelivr.net/npm/tw-elements/css/tw-elements.min.css" />
  <script src="https://cdn.tailwindcss.com/3.3.0"></script>
  <script>
    tailwind.config = {
      darkMode: "class",
      theme: {
        fontFamily: {
          sans: ["Roboto", "sans-serif"],
          body: ["Roboto", "sans-serif"],
          mono: ["ui-monospace", "monospace"],
        },
      },
      corePlugins: {
        preflight: false,
      },
    };
  </script>
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.1.1/css/all.min.css">
    <script src="https://unpkg.com/htmx.org@1.9.12" integrity="sha384-ujb1lZYygJmzgSwoxRggbCHcjc0rB2XoQrxeTUQyRjrOnlCoYta87iKBWq3EsdM2" crossorigin="anonymous"></script>
</head>

<body class="bg-gray-900 text-gray-200">
<div class="flex flex-col min-h-screen">

<nav class="bg-gray-800 shadow-lg">
    <div class="container mx-auto px-4 py-4">
        <div class="flex items-center justify-between">
            <div class="flex items-center">
                <a href="/" class="text-white text-xl font-bold"><img src="https://github.com/go-skynet/LocalAI/assets/2420543/0966aa2a-166e-4f99-a3e5-6c915fc997dd" alt="LocalAI Logo" class="h-10 mr-3 border-2 border-gray-300 shadow rounded"></a>
                <a href="/" class="text-white text-xl font-bold">LocalAI</a>
            </div>
            <!-- Menu button for small screens -->
            <div class="lg:hidden">
                <button id="menu-toggle" class="text-gray-400 hover:text-white focus:outline-none">
                    <i class="fas fa-bars fa-lg"></i>
                </button>
            </div>
            <!-- Navigation links -->
            <div class="hidden lg:flex lg:items-center lg:justify-end lg:flex-1 lg:w-0">
                <a href="https://localai.io" class="text-gray-400 hover:text-white px-3 py-2 rounded" target="_blank" ><i class="fas fa-book-reader pr-2"></i> Documentation</a>
            </div>
        </div>
        <!-- Collapsible menu for small screens -->
        <div class="hidden lg:hidden" id="mobile-menu">
            <div class="pt-4 pb-3 border-t border-gray-700">
                
                <a href="https://localai.io" class="block text-gray-400 hover:text-white px-3 py-2 rounded mt-1" target="_blank" ><i class="fas fa-book-reader pr-2"></i> Documentation</a>
               
            </div>
        </div>
    </div>
</nav>

<style>
  .is-hidden {
	display: none;
	  }
</style>

<div class="container mx-auto px-4 flex-grow">

<div class="models mt-12">
	<h2 class="text-center text-3xl font-semibold text-gray-100">
	LocalAI model gallery list </h2><br>

	<h2 class="text-center text-3xl font-semibold text-gray-100">

	 🖼️ Available {{.AvailableModels}} models</i> repositories     <a href="https://localai.io/models/" target="_blank" >
			<i class="fas fa-circle-info pr-2"></i>
		</a></h2> 

	<h3>	  
	Refer to <a href="https://localai.io/models" target=_blank> Model gallery</a> for more information on how to use the models with LocalAI.

	You can install models with the CLI command <code>local-ai models install <model-name></code>. or by using the WebUI.
	</h3>
  
	<input class="form-control appearance-none block w-full mt-5 px-3 py-2 text-base font-normal text-gray-300 pb-2 mb-5 bg-gray-800 bg-clip-padding border border-solid border-gray-600 rounded transition ease-in-out m-0 focus:text-gray-300 focus:bg-gray-900 focus:border-blue-500 focus:outline-none" type="search" 
	id="searchbox" placeholder="Live search keyword..">
	  <div class="dark grid grid-cols-1 grid-rows-1 md:grid-cols-3 block rounded-lg shadow-secondary-1 dark:bg-surface-dark">
		{{ range $_, $model := .Models }}
		<div class="box me-4 mb-2 block rounded-lg bg-white shadow-secondary-1  dark:bg-gray-800 dark:bg-surface-dark dark:text-white text-surface pb-2">
		<div>
		    {{ $icon := "https://upload.wikimedia.org/wikipedia/commons/6/65/No-Image-Placeholder.svg" }}
			{{ if $model.Icon }}
	  		{{ $icon = $model.Icon }}
	  		{{ end }}
			<div class="flex justify-center items-center">
				<img data-src="{{ $icon }}" alt="{{$model.Name}}" class="rounded-t-lg max-h-48 max-w-96 object-cover mt-3 lazy">
			</div>
	  		<div class="p-6 text-surface dark:text-white">
				<h5 class="mb-2 text-xl font-medium leading-tight">{{$model.Name}}</h5>
				
				   
				<p class="mb-4 text-base truncate">{{ $model.Description }}</p>
		
			</div>
			<div class="px-6 pt-4 pb-2">

      <!-- Modal toggle -->
      <button data-modal-target="{{ $model.Name}}-modal" data-modal-toggle="{{ $model.Name }}-modal" class="block text-white bg-blue-700 hover:bg-blue-800 focus:ring-4 focus:outline-none focus:ring-blue-300 font-medium rounded-lg text-sm px-5 py-2.5 text-center dark:bg-blue-600 dark:hover:bg-blue-700 dark:focus:ring-blue-800" type="button">
        More info
      </button>

    <!-- Main modal -->
    <div id="{{ $model.Name}}-modal" tabindex="-1" aria-hidden="true" class="hidden overflow-y-auto overflow-x-hidden fixed top-0 right-0 left-0 z-50 justify-center items-center w-full md:inset-0 h-[calc(100%-1rem)] max-h-full">
        <div class="relative p-4 w-full max-w-2xl max-h-full">
            <!-- Modal content -->
            <div class="relative bg-white rounded-lg shadow dark:bg-gray-700">
                <!-- Modal header -->
                <div class="flex items-center justify-between p-4 md:p-5 border-b rounded-t dark:border-gray-600">
                    <h3 class="text-xl font-semibold text-gray-900 dark:text-white">
                        {{ $model.Name}}
                    </h3>
                    <button type="button" class="text-gray-400 bg-transparent hover:bg-gray-200 hover:text-gray-900 rounded-lg text-sm w-8 h-8 ms-auto inline-flex justify-center items-center dark:hover:bg-gray-600 dark:hover:text-white" data-modal-hide="{{$model.Name}}-modal">
                        <svg class="w-3 h-3" aria-hidden="true" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 14 14">
                            <path stroke="currentColor" stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="m1 1 6 6m0 0 6 6M7 7l6-6M7 7l-6 6"/>
                        </svg>
                        <span class="sr-only">Close modal</span>
                    </button>
                </div>
                <!-- Modal body -->
                <div class="p-4 md:p-5 space-y-4">
                    <div class="flex justify-center items-center">
                    <img data-src="{{ $icon }}" alt="{{$model.Name}}" class="lazy rounded-t-lg max-h-48 max-w-96 object-cover mt-3">
                  </div>

                    <p class="text-base leading-relaxed text-gray-500 dark:text-gray-400">
                    {{ $model.Description }}

                    </p>
                    
                    <p class="text-base leading-relaxed text-gray-500 dark:text-gray-400">
                    To install the model with the CLI, run: <br>
                    <code> local-ai models install {{$model.Name}} </code> <br>

                    <hr>
                    See also <a href="https://localai.io/models/" target="_blank" >
                    Installation <i class="fas fa-circle-info pr-2"></i>
                    </a> to see how to install models with the REST API.
                    </p>

                    <p class="text-base leading-relaxed text-gray-500 dark:text-gray-400">
                    <ul>
                    {{ range $_, $u := $model.URLs }}
                    <li><a href="{{ $u }}" target=_blank><i class="fa-solid fa-link"></i> {{ $u }}</a></li>
                    {{ end }}  
                    </ul>
                    </p>
                </div>
                <!-- Modal footer -->
                <div class="flex items-center p-4 md:p-5 border-t border-gray-200 rounded-b dark:border-gray-600">
                    <button data-modal-hide="{{ $model.Name}}-modal" type="button" class="py-2.5 px-5 ms-3 text-sm font-medium text-gray-900 focus:outline-none bg-white rounded-lg border border-gray-200 hover:bg-gray-100 hover:text-blue-700 focus:z-10 focus:ring-4 focus:ring-gray-100 dark:focus:ring-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:border-gray-600 dark:hover:text-white dark:hover:bg-gray-700">Close</button>
                </div>
            </div>
        </div>
    </div>


			</div>
		</div>
		</div>
		{{ end }}      

		</div>
  </div>
</div>

<script>
var lazyLoadInstance = new LazyLoad({
  // Your custom settings go here
});

let cards = document.querySelectorAll('.box')
    
function liveSearch() {
    let search_query = document.getElementById("searchbox").value;
    
    //Use innerText if all contents are visible
    //Use textContent for including hidden elements
    for (var i = 0; i < cards.length; i++) {
        if(cards[i].textContent.toLowerCase()
                .includes(search_query.toLowerCase())) {
            cards[i].classList.remove("is-hidden");
        } else {
            cards[i].classList.add("is-hidden");
        }
    }
}

//A little delay
let typingTimer;               
let typeInterval = 500;  
let searchInput = document.getElementById('searchbox');

searchInput.addEventListener('keyup', () => {
    clearTimeout(typingTimer);
    typingTimer = setTimeout(liveSearch, typeInterval);
});
</script>

</div>

<script src="https://cdnjs.cloudflare.com/ajax/libs/flowbite/2.3.0/flowbite.min.js"></script>
</body>
</html>
`

type GalleryModel struct {
	Name        string   `json:"name" yaml:"name"`
	URLs        []string `json:"urls" yaml:"urls"`
	Icon        string   `json:"icon" yaml:"icon"`
	Description string   `json:"description" yaml:"description"`
}

func main() {
	// read the YAML file which contains the models

	f, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		fmt.Println("Error reading file:", err)
		return
	}

	models := []*GalleryModel{}
	err = yaml.Unmarshal(f, &models)
	if err != nil {
		// write to stderr
		os.Stderr.WriteString("Error unmarshaling YAML: " + err.Error() + "\n")
		return
	}

	// render the template
	data := struct {
		Models          []*GalleryModel
		AvailableModels int
	}{
		Models:          models,
		AvailableModels: len(models),
	}
	tmpl := template.Must(template.New("modelPage").Parse(modelPageTemplate))

	err = tmpl.Execute(os.Stdout, data)
	if err != nil {
		fmt.Println("Error executing template:", err)
		return
	}
}
