package canonical

// Parser parses a format's request bytes into a UnifiedChatRequest.
type Parser func(rawJSON []byte) (*UnifiedChatRequest, error)

// Emitter emits a UnifiedChatRequest into a format's request bytes.
type Emitter func(req *UnifiedChatRequest) ([]byte, error)

// ResponseParser parses format-specific non-stream response bytes into a UnifiedChatResponse.
type ResponseParser func(rawJSON []byte) (*UnifiedChatResponse, error)

// ResponseEmitter emits a UnifiedChatResponse into format-specific non-stream response bytes.
type ResponseEmitter func(resp *UnifiedChatResponse) ([]byte, error)

// StreamEventParser parses format-specific streaming event bytes (SSE or NDJSON line) into a UnifiedEvent.
type StreamEventParser func(rawLine []byte) (*UnifiedEvent, error)

// StreamEventEmitter emits a UnifiedEvent into format-specific streaming event bytes.
type StreamEventEmitter func(event *UnifiedEvent) ([]byte, error)

// Registry maps format names to their Parser and Emitter functions.
type Registry struct {
	parsers             map[string]Parser
	emitters            map[string]Emitter
	responseParsers     map[string]ResponseParser
	responseEmitters    map[string]ResponseEmitter
	streamEventParsers  map[string]StreamEventParser
	streamEventEmitters map[string]StreamEventEmitter
}

var DefaultRegistry = &Registry{
	parsers:             make(map[string]Parser),
	emitters:            make(map[string]Emitter),
	responseParsers:     make(map[string]ResponseParser),
	responseEmitters:    make(map[string]ResponseEmitter),
	streamEventParsers:  make(map[string]StreamEventParser),
	streamEventEmitters: make(map[string]StreamEventEmitter),
}

func RegisterParser(format string, parser Parser) {
	DefaultRegistry.parsers[format] = parser
}

func RegisterEmitter(format string, emitter Emitter) {
	DefaultRegistry.emitters[format] = emitter
}

func RegisterResponseParser(format string, parser ResponseParser) {
	DefaultRegistry.responseParsers[format] = parser
}

func RegisterResponseEmitter(format string, emitter ResponseEmitter) {
	DefaultRegistry.responseEmitters[format] = emitter
}

func RegisterStreamEventParser(format string, parser StreamEventParser) {
	DefaultRegistry.streamEventParsers[format] = parser
}

func RegisterStreamEventEmitters(format string, emitter StreamEventEmitter) {
	DefaultRegistry.streamEventEmitters[format] = emitter
}

func GetParser(format string) (Parser, bool) {
	p, ok := DefaultRegistry.parsers[format]
	return p, ok
}

func GetEmitter(format string) (Emitter, bool) {
	e, ok := DefaultRegistry.emitters[format]
	return e, ok
}

func GetResponseParser(format string) (ResponseParser, bool) {
	rp, ok := DefaultRegistry.responseParsers[format]
	return rp, ok
}

func GetResponseEmitter(format string) (ResponseEmitter, bool) {
	re, ok := DefaultRegistry.responseEmitters[format]
	return re, ok
}

func GetStreamEventParser(format string) (StreamEventParser, bool) {
	sep, ok := DefaultRegistry.streamEventParsers[format]
	return sep, ok
}

func GetStreamEventEmitter(format string) (StreamEventEmitter, bool) {
	see, ok := DefaultRegistry.streamEventEmitters[format]
	return see, ok
}
