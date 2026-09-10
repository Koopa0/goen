# Storefront media

Generated storefront media is embedded into the Go binary and served by the
versioned `/static/` asset handler.

- `products/` contains files named by `product_images.storage_key`.
- `hero/` contains home-page hero artwork.
- `*-400.webp`, `*-800.webp`, and the hero `*-720.webp` files are responsive
  derivatives; the unsuffixed storage-key files remain the source assets.

The required filenames and source dimensions are recorded in
`docs/media-requirements.md`.
