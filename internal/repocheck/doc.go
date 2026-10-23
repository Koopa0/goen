// Package repocheck holds the repository's policy tests: what CI runs and how,
// which commits may merge, the layout gate's own locks, that every named test
// exists, that every integration suite uses the migration runner, which package
// may import which, that no package is named for a layer, that every test name
// follows the naming convention, that every package comment begins "Package
// <name>", that no exported identifier is left unreferenced, that LIKE escaping
// and constraint names live only in internal/db, that no error string formats a
// person's address, and the source guards that read every template and view model
// under internal/ (the aria, form, link, nav and view-model-field rules), so a
// template moved into a feature package stays inside their reach. It has no code;
// a rule that concerns one package's own source stays in that package.
package repocheck
