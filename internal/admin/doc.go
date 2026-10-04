// Package admin is goen's back office, served over a pool that does SET ROLE admin,
// which still has no direct write access to money, ledgers or stock_quantity.
// Each desk is a package below this one.
package admin
