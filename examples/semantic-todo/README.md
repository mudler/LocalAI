This demonstrates the vector store backend in its simplest form. 
You can add tasks and then search/sort them using the TUI. 

To build and run do

```bash
$ go get .
$ go run .
```

A separate LocaAI instance is required of course. For e.g.

```bash
$ docker run -e DEBUG=true --rm -it -p 8080:8080 <LocalAI-image> bert-cpp
```
