// Package shipping is where a parcel goes and who carries it: the closed set of
// destinations a shipping method delivers to (Destination), the convenience-store
// chains that can receive an order (Chain), and the companies that carry a parcel
// to a customer (Carrier) with their public tracking pages. Each is a stable wire
// identity, never a display name; a carrier's display name is a key in
// internal/i18n, which Carrier.Name renders.
//
// Chain and Carrier are two types because they are two facts: a chain is where
// the customer collects, a carrier is who drives there. A store chain implies its
// same-named carrier (Chain.Carrier). The package imports nothing from goen but
// the message catalogue, so any package can depend on it without a cycle.
package shipping
