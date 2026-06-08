package bridge

// InjectBootstrapForTest exposes injectBootstrap to black-box tests.
func InjectBootstrapForTest(html, boot string) string { return injectBootstrap(html, boot) }

// OriginAllowedForTest exposes originAllowed to black-box tests.
func OriginAllowedForTest(origin, port string) bool { return originAllowed(origin, port) }
