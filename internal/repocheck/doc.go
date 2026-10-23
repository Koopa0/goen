// Package repocheck holds the repository's policy tests.
//
// It has no code. Each test reads the repository and fails when a rule is
// broken; a rule that concerns one package's own source stays in that package.
// The rules are that:
//
//   - CI runs what it says it runs, and how: the workflows, their path
//     filters and tool pins, and the ruleset's gates;
//   - commits that may merge carry no forbidden attribution;
//   - the layout gate's own locks hold;
//   - every named test exists, and every integration suite uses the migration
//     runner;
//   - a test that waits for a lock reads the server's activity view only
//     through dbtest;
//   - only listed tests read goen's stylesheets and scripts as text;
//   - a Makefile .env never overrides the caller's environment;
//   - each package imports only what its place in the import graph allows, and
//     no package is named for a layer;
//   - every test name follows the naming convention, every package comment
//     begins "Package <name>", and no exported identifier is left unreferenced;
//   - LIKE escaping and constraint names live only in internal/db, bearer
//     tokens are minted and digested only in internal/bearer, and no error
//     string formats a person's address;
//   - the source guards that read every template and view model under
//     internal/ hold (the aria, form, link, nav and view-model-field rules), so
//     a template moved into a feature package stays inside their reach.
package repocheck
