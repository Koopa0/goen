# Storefront media

Generated storefront media is embedded into the Go binary and served by the
versioned `/static/` asset handler.

- `products/` contains files named by `product_images.storage_key`, as
  `<slug>-NN.webp`: a 1600x1200 source on a `#f9f9f9` ground.
- `campaign-banner-01.webp` (1600x600) is in `products/` because a campaign's
  `image_key` is resolved by the same function as a product image's storage key.
- `hero/` contains home-page hero artwork (1440x720, 2:1).
- `products/campaign-*.webp` are
  campaign headers (1600x600). `products/department-<slug>.webp` (800px, with a
  `-400` rendition) are the department photographs, in `products/` because a
  department's `image_key` is resolved like a product's storage key, which names
  no folder; the home tiles read them by slug.
- `*-400.webp`, `*-800.webp`, and the hero `*-720.webp` files are responsive
  derivatives; the unsuffixed storage-key files remain the source assets.
  The repo has no script that creates derivatives for a new photo, so a new
  photo's -400/-800 files are produced by hand.
