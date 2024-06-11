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
  <title>LocalAI model gallery</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bulma@0.9.2/css/bulma.min.css">
</head>
<body>
<style>
  .is-hidden {
	display: none;
	  }
</style>
<div class="columns mx-1">
  <div class="column is-half is-offset-one-quarter">

    <section class="hero is-primary mb-4">
      <div class="hero-body">
        <p class="title">
          LocalAI model gallery list
        </p>
        <p class="subtitle">
    Here is a glimpse of the models you can find in LocalAI. To use the models, you can download them from the LocalAI app.
        </p>
      </div>
    </section>
  
    
    <section>
		<div>
			<label for="searchbox" class="is-size-5">Search</label>
			<input class='input mb-5' type="search" 
					id="searchbox" placeholder="Live search keyword..">
		</div>

		{{ range $_, $model := .Models }}
		<div class="box">
			<strong>{{$model.Name}}</strong>
			<img src="{{$model.Icon}}" alt="{{$model.Name}}" class="mb-3">
			{{ range $_, $u := $model.URLs }}
			<p>{{ $u }}</p>
			{{ end }}      
			<p>{{ $model.Description }}</p>
	  		<a href="http://localhost:8080/browse?term={{ $model.Name}}" class="button is-primary">Install in LocalAI ( instance at localhost:8080 )</a>
		</div>
		{{ end }}      
    </section>

  </div>
</div>

<script>
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
		Models []*GalleryModel
	}{
		Models: models,
	}
	tmpl := template.Must(template.New("modelPage").Parse(modelPageTemplate))

	err = tmpl.Execute(os.Stdout, data)
	if err != nil {
		fmt.Println("Error executing template:", err)
		return
	}
}
