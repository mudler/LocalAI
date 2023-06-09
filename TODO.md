# Experimental OpenAi/OpenAPI Branch

## This branch is currently a WORK IN PROGRESS.

A few notes up front:
* This is _really_ not ready to merge yet. It is being published for early feedback and to gauge interest.
* Don't take the apiv2 package name personally - I wasn't sure what characters were allowed in the package names... and this worked.
* This is a Vertical Slice POC / Demo - so far, only text requests have really been considered, though some early prototyping has been done. Even then, testing has been limited to ggml-gpt4all-j since the goal is to get something out there!
* This document started as my personal notes, and it's still pretty poorly written.
* Model gallery is cool, and probably will help a lot with the more verbose config files this POC uses... but getting that up and running is out of scope until most of the endpoints work.

## TODO Notes:
* CURRENTLY BROKEN to investigate merging YAML 


## Goals And Documentation


### The original goal of this branch is to create POC of generating request models and endpoints that exactly match OpenAI's public specifications, allowing us to easily maintain "compatiblity", even if that means returning "not implemented" server errrors.

Immediately, there's a few problems with that goal:

* Local LLM backends may require additional configuration fields that OpenAI does not accept or require
* Initialization parameters like threadcount and path to the model weights are not really even proper to set per-request, so a second level of configration is desirable.
* The current api uses a custom request model that lumps together properties from different endpoints. It works, but it's somewhat confusing to know what features are supported and how

I propose instead a solution along the lines of this very raw POC, in which we define an optional extension to the request models along the name of `x-localai-extensions`. Additionally, we would change our configuration model to strictly bind together the tuple of an identifying registration, the request defaults (x-localai-extensions and all), and any other settings we need to stash.

Clients dependent on the official OpenAI clients can therefore send specific model names to specific endpoints to retrieve different combinations of advanced settings - and clients who optionally want to stay in tune with the most features can generate clients based off our specifications.

These parameters are currently defined in the file `./openai-openapi/localai_model_patches.yaml`.
Rather than use a pure text patch, this is implemented via YTT (link), which is aware of the YAML syntax, making the matching a bit more flexible.

Once we've patched the specifications to include our additional parameters and any other tweaks, I use the oapi-codegen project (link) to generate models and server stubs based on our extended YAML specification.

A few notes about known issues here, since this _is_ an early POC:
* YTT currently has a minor, but extremely weird bug. One of the properties of OpenAI's request model is named 'n'. For some #*$@ reason, YTT reproducably mangles this to "False" on my system. I have not yet investigated why, and have implemented a shameful workaround. I'll dig into this now that this POC is off the ground.
*  I've been prototyping with net/http, since it's the _simplest_ and Fiber just landed upstream. Will test with that soon.
* Currently, we require a patched version of oapi-codegen. I've extended it to support multiple struct tags when generating structs, and added a new template extension point for some.... stuff I'll mention later.
** This isn't strictly a problem - now that my changes are settled down, it might be worth trying to upstream them. If they aren't accepted, maybe we can create a go-skynet fork to hold the patch and use the existing dependency maintence infrastructure


Once the code was generated, we now have individual models for each endpoint. While it's worth considering changing things farther along the stack now that we have this capability, my goal was to weld these disparate models to our existing flexible model loader.

Therefore, `apiv2/config.go` declares a new configuration format that's radically different from what we've used in the past. It is comprised of three main segments:

* ConfigRegistration: This structure holds the name the configuration will be registered and requested by, as well as the endpoint name this configuration applies to. It's used as a key all over the place.
* LocalSettings: This structure holds the fixed, invariant properties of a local ai backend. Includes the path to the model weights, the path to the template to use, the number of threads to use, and explicitly which backend to invoke. (Personal Opinion Warning: I've intentionally not used greedyloader at all)
* RequestDefaults: Since our registrations / configuration files are specific to the tuple of {Endpoint, Model}, this property should contain any keys the user wants or needs to default on those requests. Whether it's a stock property of the OpenAI spec or one of our extensions, this is "just" the request model.

By using the mapstructure library, I'm able to inflate these varied shapes to a generic structure, with a common interface.

This common interface is responsible for decoding down to the relevant fields. Currently in this proof of concept state, I'm heavily leveraging llama.PredictOptions, as it's the most feature complete. If we take this POC to completion, I might suggest that we expend the effort to create a base package all the go-* backends can inherit from, rather than this extra copy / mapstructure step 
* Currently doesn't even work, and is commented out. Plan there was to also going to require modifying go-* libs to introduce an additional constructor that takes the options structure rather than slice of fn. 

config manager is conceptually similar to the config_merger of the existing code

`localai.go` contains the method stubs our generated server will call, and additionally contains the code for merging together the configuration file + the request input.

Actually interfacing the with backends is currently living in `engine.go`, which is conceptually similar to `prediction.go` although it is not a 1:1 port.