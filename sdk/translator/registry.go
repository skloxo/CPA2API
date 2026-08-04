package translator

import (
	"context"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/translator/canonical"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Registry manages translation functions across schemas.
type Registry struct {
	mu        sync.RWMutex
	requests  map[Format]map[Format]RequestTransform
	responses map[Format]map[Format]ResponseTransform
}

// NewRegistry constructs an empty translator registry.
func NewRegistry() *Registry {
	return &Registry{
		requests:  make(map[Format]map[Format]RequestTransform),
		responses: make(map[Format]map[Format]ResponseTransform),
	}
}

// Register stores request/response transforms between two formats.
func (r *Registry) Register(from, to Format, request RequestTransform, response ResponseTransform) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.requests[from]; !ok {
		r.requests[from] = make(map[Format]RequestTransform)
	}
	if request != nil {
		r.requests[from][to] = request
	}

	if _, ok := r.responses[from]; !ok {
		r.responses[from] = make(map[Format]ResponseTransform)
	}
	r.responses[from][to] = response
}

// TranslateRequest converts a payload between schemas, returning the original payload
// if no translator is registered. When falling back to the original payload, the
// "model" field is still updated to match the resolved model name so that
// client-side prefixes (e.g. "copilot/gpt-5-mini") are not leaked upstream.
func (r *Registry) TranslateRequest(from, to Format, model string, rawJSON []byte, stream bool) []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if UseCanonical {
		p, okParser := canonical.GetParser(from.String())
		e, okEmitter := canonical.GetEmitter(to.String())
		if okParser && okEmitter {
			unifiedReq, err := p(rawJSON)
			if err == nil {
				if model != "" {
					unifiedReq.Model = model
				}
				unifiedReq.Stream = stream
				translated, err := e(unifiedReq)
				if err == nil {
					return translated
				} else {
					log.Warnf("canonical: failed to emit request from IR: %v", err)
				}
			} else {
				log.Warnf("canonical: failed to parse request to IR: %v", err)
			}
		}
	}

	if byTarget, ok := r.requests[from]; ok {
		if fn, isOk := byTarget[to]; isOk && fn != nil {
			return fn(model, rawJSON, stream)
		}
	}
	if model != "" && gjson.GetBytes(rawJSON, "model").String() != model {
		if updated, err := sjson.SetBytes(rawJSON, "model", model); err != nil {
			log.Warnf("translator: failed to normalize model in request fallback: %v", err)
		} else {
			return updated
		}
	}
	return rawJSON
}

// HasResponseTransformer indicates whether a response translator exists.
func (r *Registry) HasResponseTransformer(from, to Format) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if UseCanonical {
		_, okP := canonical.GetResponseParser(from.String())
		_, okE := canonical.GetResponseEmitter(to.String())
		if okP && okE {
			return true
		}
		_, okSP := canonical.GetStreamEventParser(from.String())
		_, okSE := canonical.GetStreamEventEmitter(to.String())
		if okSP && okSE {
			return true
		}
	}

	if byTarget, ok := r.responses[from]; ok {
		if _, isOk := byTarget[to]; isOk {
			return true
		}
	}
	return false
}

// TranslateStream applies the registered streaming response translator.
func (r *Registry) TranslateStream(ctx context.Context, from, to Format, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) [][]byte {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if UseCanonical {
		p, okParser := canonical.GetStreamEventParser(from.String())
		e, okEmitter := canonical.GetStreamEventEmitter(to.String())
		if okParser && okEmitter {
			unifiedEvent, err := p(rawJSON)
			if err == nil && unifiedEvent != nil {
				if model != "" && unifiedEvent.FinishReason != "done_marker" {
					unifiedEvent.Model = model
				}
				translated, err := e(unifiedEvent)
				if err == nil {
					return [][]byte{translated}
				} else {
					log.Warnf("canonical: failed to emit stream event from IR: %v", err)
				}
			} else if err != nil {
				log.Warnf("canonical: failed to parse stream event to IR: %v", err)
			} else {
				return [][]byte{}
			}
		}
	}

	if byTarget, ok := r.responses[to]; ok {
		if fn, isOk := byTarget[from]; isOk && fn.Stream != nil {
			return fn.Stream(ctx, model, originalRequestRawJSON, requestRawJSON, rawJSON, param)
		}
	}
	return [][]byte{rawJSON}
}

// TranslateNonStream applies the registered non-stream response translator.
func (r *Registry) TranslateNonStream(ctx context.Context, from, to Format, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if UseCanonical {
		p, okParser := canonical.GetResponseParser(from.String())
		e, okEmitter := canonical.GetResponseEmitter(to.String())
		if okParser && okEmitter {
			unifiedResp, err := p(rawJSON)
			if err == nil {
				if model != "" {
					unifiedResp.Model = model
				}
				translated, err := e(unifiedResp)
				if err == nil {
					return translated
				} else {
					log.Warnf("canonical: failed to emit response from IR: %v", err)
				}
			} else {
				log.Warnf("canonical: failed to parse response to IR: %v", err)
			}
		}
	}

	if byTarget, ok := r.responses[to]; ok {
		if fn, isOk := byTarget[from]; isOk && fn.NonStream != nil {
			return fn.NonStream(ctx, model, originalRequestRawJSON, requestRawJSON, rawJSON, param)
		}
	}
	return rawJSON
}

// TranslateTokenCount applies the registered token count response translator.
func (r *Registry) TranslateTokenCount(ctx context.Context, from, to Format, count int64, rawJSON []byte) []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if byTarget, ok := r.responses[to]; ok {
		if fn, isOk := byTarget[from]; isOk && fn.TokenCount != nil {
			return fn.TokenCount(ctx, count)
		}
	}
	return rawJSON
}

// UseCanonical is a package-level global toggle to enable the Canonical IR translation pipeline.
var UseCanonical bool

var defaultRegistry = NewRegistry()

// Default exposes the package-level registry for shared use.
func Default() *Registry {
	return defaultRegistry
}

// Register attaches transforms to the default registry.
func Register(from, to Format, request RequestTransform, response ResponseTransform) {
	defaultRegistry.Register(from, to, request, response)
}

// TranslateRequest is a helper on the default registry.
func TranslateRequest(from, to Format, model string, rawJSON []byte, stream bool) []byte {
	return defaultRegistry.TranslateRequest(from, to, model, rawJSON, stream)
}

// HasResponseTransformer inspects the default registry.
func HasResponseTransformer(from, to Format) bool {
	return defaultRegistry.HasResponseTransformer(from, to)
}

// TranslateStream is a helper on the default registry.
func TranslateStream(ctx context.Context, from, to Format, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) [][]byte {
	return defaultRegistry.TranslateStream(ctx, from, to, model, originalRequestRawJSON, requestRawJSON, rawJSON, param)
}

// TranslateNonStream is a helper on the default registry.
func TranslateNonStream(ctx context.Context, from, to Format, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []byte {
	return defaultRegistry.TranslateNonStream(ctx, from, to, model, originalRequestRawJSON, requestRawJSON, rawJSON, param)
}

// TranslateTokenCount is a helper on the default registry.
func TranslateTokenCount(ctx context.Context, from, to Format, count int64, rawJSON []byte) []byte {
	return defaultRegistry.TranslateTokenCount(ctx, from, to, count, rawJSON)
}
