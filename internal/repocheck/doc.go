// Package repocheck holds the repository's policy tests: what CI runs and how,
// which commits may merge, the layout gate's own locks, that every named test
// exists, that every integration suite uses the migration runner, and the
// source guards that read every template and view model under internal/ (the
// aria, form, link, nav and view-model-field rules), so a template moved into
// a feature package stays inside their reach. It has no code; a rule that
// concerns one package's own source stays in that package.
package repocheck
