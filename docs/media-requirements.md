# 圖檔需求(給生圖用)

批次 ① 首頁目前所有圖片都是佔位。這份文件列出**要生哪些檔、什麼尺寸、什麼調性**。
檔名不是建議 —— `product_images.storage_key` 已經在 `seed/dev_catalog.sql` 裡宣告,
生出來的檔名必須一字不差對上,否則 seed 要一起改。

## 媒體管線

圖檔放進下方指定的 `assets/media/` 目錄後會嵌入 Go binary,由既有的內容雜湊
`/static/` handler 提供。`product_images.storage_key` 必須是單一檔名,首頁會把它映射
成 `/static/media/products/{storage_key}`；主視覺則固定讀取
`/static/media/hero/home-hero-01.webp`。每張商品原圖另有 `-400`、`-800` 衍生檔,hero
另有 `home-hero-01-720.webp`；模板用實際檔案輸出 `srcset` 與對應的 `sizes`,並保留原圖
固有尺寸。所有 URL 都附內容雜湊 query 以便長效快取。

## 一、商品圖(15 張,必要)

### 尺寸

**1600 × 1200,4:3 橫向,WebP。**

seed 目前宣告的是 `1200x1200` 方形 —— 那是錯的,或者說跟版面不一致:設計系統的
`.ui-product__media` 是 `aspect-ratio: 4 / 3` 且 `object-fit: cover`,方形圖進去會被
裁掉上下各 12.5%,商品可能被切到。改成 4:3 出圖,`dev_catalog.sql` 的 width/height 我
會跟著改。

實際版面盒子(用 `Emulation.setDeviceMetricsOverride` 量的,不是猜的):

| 視窗寬 | 商品卡寬 | 圖片盒 |
|---|---|---|
| 375 | 166.5 px | 166.5 × 125 |
| 768 | 224.3 px | 224.3 × 168 |
| 1440 | 308 px | 308 × 231 |

最大需求是 308 × 231,在 2× DPR 下是 616 × 462。出 1600 × 1200 有足夠餘裕給未來的
PDP 大圖與 zoom,不必為首頁單獨再出一版。

### 檔名(必須完全一致)

| 檔名 | 商品 | 類別 |
|---|---|---|
| `pixelight-9-01.webp` | Pixelight 9 5G | 手機 |
| `pixelight-9-pro-01.webp` | Pixelight 9 Pro 5G | 手機 |
| `aurora-edge-7-01.webp` | Aurora Edge 7 | 手機 |
| `meridian-book-14-01.webp` | Meridian Book 14 | 筆電 |
| `meridian-book-16-pro-01.webp` | Meridian Book 16 Pro | 筆電 |
| `koto-pad-mini-01.webp` | Koto Pad mini | 平板 |
| `aurora-slate-11-01.webp` | Aurora Slate 11 | 平板 |
| `nimbus-buds-pro-01.webp` | Nimbus Buds Pro | 耳機 |
| `koto-over-ear-01.webp` | Koto Over-Ear 靜 | 耳機(罩耳式) |
| `nimbus-band-2-01.webp` | Nimbus Band 2 | 穿戴(手環) |
| `meridian-watch-s3-01.webp` | Meridian Watch S3 | 穿戴(手錶) |
| `aurora-charger-65-01.webp` | Aurora GaN 65W 充電器 | 充電 |
| `koto-cable-braided-01.webp` | Koto 編織 USB-C 線 2m | 線材 |
| `pixelight-9-pro-case-01.webp` | Pixelight 9 Pro 保護殼 | 保護殼 |
| `meridian-book-sleeve-14-01.webp` | Meridian 筆電內袋 14" | 收納 |

`-01` 是圖片序號。之後同商品的第二、三張沿用 `-02`、`-03`,首頁只取 `position` 最小
的那張。

### 調性(這是最重要的一段)

15 張圖會**同時出現在一個格線裡**,所以彼此的一致性比單張好看重要得多。一張背景偏
暖、一張偏冷,格線就散了。

- **背景:單一純色,暖中性灰白**。對齊設計系統的 `--elevated` token,在淺色模式下大
  約 `oklch(0.945 0.006 107)` ≈ `#efeeea`。**每一張都要用同一個背景色**,不要漸層、
  不要情境照、不要桌面擺拍。
- **打光:柔和頂光偏左,單一光源**,陰影短而軟,落在商品正下方偏右。不要硬陰影、不
  要鏡面倒影。
- **視角:正面微俯角(約 15°)**,手機/平板/筆電一律直立或半開,不要 45° 斜擺。同類
  商品之間視角要一致。
- **佔比:商品佔畫面高度約 75%**,四周留白均勻。小配件(線材、充電頭)不要因為體積
  小就放到只剩一點,要放大到同樣的視覺重量。
- **不要**:文字、浮水印、品牌 logo(這些是虛構品牌)、人手、包裝盒、價格標、
  「NEW」之類的角標(那些是 DOM 畫的 badge)。
- **色彩**:商品本體可以有顏色,但整體飽和度壓低,不要鮮豔到蓋過 accent 色
  (cyan-teal `oklch(0.55 0.09 210)`)。

品牌是虛構的(Pixelight / Meridian / Aurora / Koto / Nimbus),所以**不要**畫成任何真
實品牌的外觀 —— 不要 Apple、Samsung、Sony 的辨識性設計語言。做成通用、乾淨的現代 3C
外型即可。

### alt 文字

seed 已經有了,格式是 `{商品名} 商品照`,例如 `Pixelight 9 5G 商品照`。生圖時不用管,
但如果你要改成更描述性的 alt(對無障礙更好),告訴我,我改 seed。

## 二、首頁主視覺(1 張,必要)

| 項目 | 值 |
|---|---|
| 檔名 | `home-hero-01.webp` |
| 尺寸 | **1440 × 900**(桌機盒子量到 639 × 380,2× 是 1278 × 760;出 1440 × 900 留裁切餘裕) |
| 比例 | 桌機約 1.68:1,手機裁成 341 × 180(約 1.9:1) |

**構圖必須「中間留白、內容偏一側」** —— 因為手機版是圖在上、文字在下,桌機版是左文字
右圖,兩種裁切都會切掉不同的邊。主體放在**右側 2/3 的中央**,左邊和上下都要留可裁的
安全區。

調性:同一套暖中性背景,放 2–3 件旗艦商品(手機 + 筆電 + 耳機)的群組擺拍,同樣柔光、
同樣低飽和。**不要放文字**,標題「挑一台好的,值得。」是 DOM 畫的。

## 三、分類圖示(不用生)

首頁的 6 個分類磚用的是 `internal/ui/icons/icons.templ` 裡的 inline SVG(Lucide 風格、
stroke 1.5),已經完成。設計系統明確禁止第二套圖示系統,所以**不要**生分類圖。

## 四、可選(現在不急)

| 檔名 | 用途 | 尺寸 |
|---|---|---|
| `og-default.png` | 社群分享縮圖(目前沒有 `og:image`) | 1200 × 630 |
| `home-hero-02.webp`、`-03` | 設計稿的 hero 是 2–3 張輪播,目前只做一張 | 同 hero |

輪播現在**刻意沒做** —— 只有一個 hero,做輪播控制項會是一個沒有第二張可切的按鈕。要
做的話先給我第 2、3 張。

## 交付方式

放到 `assets/media/products/` 與 `assets/media/hero/`(目錄還不存在,接 media pipeline
時建)。或者給我一個資料夾,我來接。

WebP,品質 80–85,每張控制在 200 KB 以內。如果生圖只能出 PNG/JPEG,給原檔即可,我來
轉 WebP 並壓縮。
