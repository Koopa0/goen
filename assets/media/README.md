# Storefront media

Generated storefront media is embedded into the Go binary and served by the
versioned `/static/` asset handler.

- `products/` contains files named by `product_images.storage_key`, as
  `<slug>-NN.webp`: a 1600x1200 source on a `#f9f9f9` ground.
- `hero/` contains home-page hero artwork.
- `*-400.webp`, `*-800.webp`, and the hero `*-720.webp` files are responsive
  derivatives; the unsuffixed storage-key files remain the source assets.
  `scripts/recolour-ground.py` produces the product derivatives.
