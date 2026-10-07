# Three-digit postal-code districts

`districts.csv` contains all 371 administrative-district rows (368 distinct
three-digit postal codes) from Chunghwa Post's **3碼郵遞區號與行政區中心點經緯度對照表**,
provided through [government dataset 25489](https://data.gov.tw/dataset/25489).
The dataset declares the [Government Data Open License, version 1](https://data.gov.tw/license),
with Chunghwa Post as the provider. Retrieved on 2026-10-06; the dataset metadata
shows an update time of 2023-06-27. This is a checked-in snapshot, not a live feed.

Source XML:
[1050812_行政區經緯度(toPost).xml](https://www.post.gov.tw/post/download/1050812_%E8%A1%8C%E6%94%BF%E5%8D%80%E7%B6%93%E7%B7%AF%E5%BA%A6%28toPost%29.xml).
Its SHA-256 at retrieval was
`5bdc716df9170166b3e62183a38b69851ca1d95208964485a3d8cc291316a8d7`.

The CSV keeps the XML's `_x0033_碼郵遞區號` and `行政區名` fields as
`prefix,district`. Rows are stably sorted by prefix; names and the order of
multiple districts for one prefix remain as published. No district is dropped
or inferred. In particular, `300` has three districts and `600` has two.
Coordinates and TGOS links are omitted because this lookup does not use them.

`Districts` accepts an exact three-digit prefix and returns a copy of every
published district name. Unknown, malformed and longer postal codes return no
names. This table describes districts; it does not change delivery-zone input
validation or decide whether an address can receive a delivery.

To refresh, download the same official XML, inspect changes in names and shared
postal codes, then derive the CSV with this transformation:

```python
import csv
import xml.etree.ElementTree as ET

root = ET.parse("districts-source.xml").getroot()
rows = [(row.find("_x0033_碼郵遞區號").text,
         row.find("行政區名").text) for row in root]
rows.sort(key=lambda row: row[0])
with open("districts.csv", "w", encoding="utf-8", newline="") as target:
    writer = csv.writer(target, lineterminator="\n")
    writer.writerow(["prefix", "district"])
    writer.writerows(rows)
```

Review the resulting diff and update the documented source hash, retrieval date,
and independent snapshot expectations in `postcode_test.go` together.
