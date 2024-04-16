# LocalAI MQTT Module

This document is not currently intended as user documentation, but development notes for now

Use the v5 MQTT request response pattern by default, fall back on generic named queues?


`/localai/request/v1/<vendor>/<serviceType>/<model>`

`/localai/response/v1/<vendor>/<serviceType>/<model>` AND ?
`/localai/response/v1/<vendor>/<serviceType>/<model>/:requestId`

vendor examples:
* openai
* localai <endpoints we make up not based on external people>
* eleventlabs
etc

serviceType examples:
* tts
* chat
* image
etc

model on request body to be overriden by the topic param


PRE V5 ideas:

topic param exists to enable per-model subscription

perhaps also have `/localai/request/v1/<vendor>/<serviceType>` that pulls model out like http requests and re-adds to the correct queue?

problem is the submitter needs to know where to look other than the general sump feed, if we want that, so requestID may need to be a guid of CLIENT CHOICE - is that the normal pattern here

SORTA RELATED:
Inside ModelLoader // BackendLoader() track thelast time called, and model name?