package middleware

import "github.com/labstack/echo/v4"

// StampUsage records the canonical token counts on the echo context so
// UsageMiddleware can attribute the request without parsing the response
// body. Handlers must call this for every successful response — the
// body-parse fallback is reserved for foreign endpoints (e.g., the cloud
// passthrough proxy).
//
// model is the name written into the response payload; passing it here
// is what lets the middleware fill the UsageRecord even when the handler
// abbreviates or rewrites the user-supplied model. Empty values are
// ignored so partial information is still useful (e.g., embeddings calls
// where completion is always 0).
//
// prompt and completion accept int because that's the native width of
// LocalAI's TokenUsage / OpenAIUsage structs (token counts never come
// close to overflow). Conversion to int64 happens once, here, so call
// sites stay free of casts.
func StampUsage(c echo.Context, model string, prompt, completion int) {
	if c == nil {
		return
	}
	if model != "" {
		c.Set(ContextKeyResponseModel, model)
	}
	p := int64(prompt)
	cp := int64(completion)
	c.Set(ContextKeyPromptTokens, p)
	c.Set(ContextKeyCompletionTokens, cp)
	c.Set(ContextKeyTotalTokens, p+cp)
}
