# htmx

`htmx.min.js` is htmx `4.0.0-beta6` with local request-ownership and history
patches. Its upstream base is
[`6ca11fbdc881a96c5fbeb0d7094a77183120ea22`](https://github.com/bigskysoftware/htmx/tree/6ca11fbdc881a96c5fbeb0d7094a77183120ea22).
The unmodified [`dist/htmx.min.js`](https://github.com/bigskysoftware/htmx/blob/6ca11fbdc881a96c5fbeb0d7094a77183120ea22/dist/htmx.min.js)
has SHA-256
`28fae7bbe8e8142b702debb9d5234a9a436d9435a4b5165b195aa1a7ed840d25`.

The upstream [license](https://github.com/bigskysoftware/htmx/blob/6ca11fbdc881a96c5fbeb0d7094a77183120ea22/LICENSE)
is BSD-0-Clause. Upstream transforms private method names in `src/htmx.js`
into JavaScript private names, then minifies with Terser; goen vendors the
result directly and has no JavaScript build.

## Local contracts

The readable upstream [`src/htmx.js`](https://github.com/bigskysoftware/htmx/blob/6ca11fbdc881a96c5fbeb0d7094a77183120ea22/src/htmx.js)
names the patched paths:

- `ReqQ.finish(ctx)` releases and drains the queue only for its current
  request. A replaced request's `finally` cannot release its replacement.
- `__issueRequest` checks cancellation after fetching the response and after
  reading its body, before response actions can act on a superseded request.
- `swap` passes the request's signal through content insertion and checks it
  after asynchronous work. A queued transition or delayed swap cannot insert
  an old response after a replacement. History and the title are committed
  when the accepted main swap inserts its content, before animation or settling
  finishes. A superseded pending swap cannot leave its URL beside old results;
  cancellation after insertion cannot discard the URL of content already shown.
  Committed CSS swaps still restore temporary attributes and process connected
  content when canceled during settling. Delayed swaps share a target-local
  count so obsolete cleanup cannot remove a replacement's busy class; the last
  completed or canceled swap releases it.
- `__resolveHistoryAction` suppresses inherited or boosted history updates
  for HTTP failures. Explicit `HX-Push-Url` and `HX-Replace-Url` instructions
  retain their semantics. Error-body swaps, including deliberate POST 422
  form rendering, are independent of that history decision.

`scripts/filter-feedback-check.mjs` exercises HTTP failure and recovery, three
replacement requests, late response bodies, deferred swaps, and committed swaps
whose animations or CSS settling have not finished, and staggered delayed swaps
against
real listing responses in both languages at 375 and 1440 pixels. It runs in
the CI layout recipe before the broader layout gate. Isolated intercepted
controls also preserve POST 422 form rendering, explicit false/push/replace
history directives, and manual swaps without a request signal; the POST
control does not submit a shop write.

## Updating the vendor

Compare a proposed upstream version's readable request queue, request, swap,
and history paths with these contracts before replacing the distribution.
Retain any missing repairs, update the immutable upstream commit and base
digest above, and run the existing native CI journeys against the replacement.
Replacing only the file's version string does not update its source.
