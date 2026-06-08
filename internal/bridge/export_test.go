package bridge

// InjectBootstrapForTest exposes injectBootstrap to black-box tests.
func InjectBootstrapForTest(html, boot string) string { return injectBootstrap(html, boot) }
