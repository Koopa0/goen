//go:build integration

package dbtest

// TemplateName lets a test read the template's catalogue row without
// connecting to it: a session on the template would stop every later copy.
const TemplateName = templateName
