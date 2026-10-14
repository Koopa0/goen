// Package repocheck holds the repository's policy tests: what CI runs and how,
// which commits may merge, the layout gate's own locks, that every named test
// exists and that every integration suite uses the migration runner. It has no
// code; a rule that concerns one package's own source stays in that package.
package repocheck
