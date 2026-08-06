# goen 品牌圖形標誌生成 Prompt — 給 Imagen 2.0(透過 Codex)

> 這份文件是專門給圖像生成模型用的 prompt/context,目的是產出 **goen** 品牌的圖形標誌(icon mark),**不包含任何文字排版**——文字部分(wordmark)另外交給真實字體處理,見文末說明。

---

## 品牌背景(生成時的概念依據)

品牌名稱 **goen**(全小寫),命名邏輯:

- **Go** —— 後端技術核心(Golang)
- **ご縁(go-en)** —— 日文真實詞彙,指人與人、人與物之間的「緣分/連結」
- **五円(go-en)** —— 與「ご縁」同音,日本神社供奉五円硬幣正是取其諧音討吉利,五円硬幣的特徵是**圓形硬幣中央有一個方形穿孔**
- 品牌本質:讓買家與對的好商品相遇

這個典故直接提供了一個現成、獨特、又有文化寓意的視覺符號:**圓中帶方孔的硬幣造型**。同時,「圓形中央方孔」這個造型在華人文化裡也會讓人聯想到古錢幣(俗稱孔方兄),所以這個符號在中日文化圈都讀得懂,不需要额外解釋。

---

## 重要限制(務必遵守)

1. **圖片裡不要出現任何文字、字母、假名或漢字。** Imagen 2.0 對於精準畫出可辨識文字的能力不穩定,尤其是日文假名,容易變成亂碼或扭曲筆畫。「goen」與「ご縁」的文字一律留到後製,用真實字體疊加,這份 prompt 只負責生成純圖形符號。
2. **只要單色/雙色的扁平向量插畫風格**,不要照片寫實、不要 3D 立體、不要玻璃感/金屬反光渲染、不要漸層、不要陰影、不要材質紋理。理由:生成出來的圖之後要重新描邊轉成乾淨的 SVG 檔,越乾淨、對比越高、線條越簡單,後製描圖才會準。
3. **構圖置中、正方形 1:1 畫布、背景乾淨(純白或單一底色)**,方便後續去背。
4. 不要抄襲或模仿任何現有品牌(尤其避免長得像真的貨幣圖案、任何國家法定貨幣的官方硬幣設計、或任何已知企業 logo)——只取「圓形+中央方孔」這個抽象幾何概念,不要真的畫成一枚寫實的日圓硬幣(不要邊緣紋路、不要看起來像金屬鑄造物,要是純粹的幾何圖形化詮釋)。

---

## 核心視覺概念

### 方向 A(主推):圓中方孔

抽象化「五円硬幣」的核心幾何特徵——一個正圓形,中央挖空一個正方形。這是整份 brief 最推薦的方向,因為它同時承載了品牌典故、且極度簡潔,在 16px favicon 尺寸下依然清楚可辨。

**可直接貼給 Imagen 2.0 的 prompt:**

```
A minimalist flat vector logo icon: a perfect circle with a small square cut out of its exact center, like an abstract geometric coin shape. Single solid color silhouette (pure black on a plain white background), clean crisp geometric lines, no gradients, no drop shadows, no 3D effect, no texture, no metallic or glossy rendering, no text or letters anywhere in the image. Centered composition, square 1:1 canvas, flat vector illustration style, high contrast, extremely simple and legible even at very small sizes like a 16px favicon. Modern minimalist tech brand aesthetic.
```

**變化版本(建議都生成,挑一個最順眼的):**

- **實心剪影版**(圓形實心黑色,中央鏤空一個方孔,方孔露出背景色):
  ```
  ...a solid filled black circle silhouette with a small square hole cut through the center revealing the white background beneath...
  ```
- **線條版**(空心圓環外框,中央一個小方孔,像徽章外框那樣輕盈):
  ```
  ...an outlined circle made of a thin even-width stroke, with a small square hole at its exact center, like a minimal line-art badge, no fill, thin uniform line weight throughout...
  ```
- **偏方孔比例實驗**(方孔可以稍微大一點更明顯,或小一點更精緻,請各生成 2–3 種比例讓我們比較):
  ```
  ...vary the size ratio of the central square hole relative to the circle: try one version where the square hole is small and subtle (about 15% of the circle's diameter), and another where it's more prominent (about 30% of the circle's diameter)...
  ```

### 方向 B(輔助,備用):交疊環形

如果方向 A 出來的圖形跟「錢幣」的聯想太強、想要更抽象表現「緣分/連結」本身而非硬幣造型,可以改用兩個輕微交疊的簡單環形,象徵兩者相遇、產生連結(呼應「ご縁」的抽象意涵,而非「五円」的具象造型)。

**可直接貼給 Imagen 2.0 的 prompt:**

```
A minimalist flat vector logo icon: two thin circular rings of equal size, overlapping slightly at one edge to form a simple abstract connection symbol (similar to a Venn diagram intersection, but with only thin outlined rings, not filled shapes). Single solid color (pure black), thin uniform line weight, no gradients, no shadows, no text or letters, plain white background, centered, square 1:1 canvas, clean geometric modern minimalist style, high contrast, legible at favicon size.
```

---

## 顏色版本的生成順序

**先只生成純黑(或深墨色 #1a1a1a)在白底上的版本**,不要一開始就套用品牌色。原因:黑白版本最適合直接拿去用向量化工具(Illustrator 的 Image Trace、或 vectorizer.ai 之類的線上工具)重新描成乾淨的 SVG;顏色之後在向量檔案階段再套用即可(例如呼應五円硬幣黃銅色調的候選色),不需要在圖像生成階段就決定顏色,那樣反而會綁死重畫的彈性。

---

## 輸出格式建議

- 正方形 1:1 構圖
- 純白背景(或至少是單一乾淨底色,方便去背)
- 方向 A 建議生成 6–8 張變化(實心版 / 線條版各半,方孔比例各 2–3 種)
- 方向 B 生成 2–3 張作為備用比較
- **拿到 Imagen 輸出的點陣圖後,務必用向量化工具重新描成乾淨的 SVG,不要直接把 AI 生成的點陣圖當最終 logo 檔案使用**——AI 生成圖邊緣通常不夠銳利平滑,重新描邊之後,logo 在網頁、favicon、各種尺寸下才能保持清晰,也才能真的做成 `templ`/CSS 裡可以直接引用的 SVG 資產。

---

## Wordmark(文字部分,不透過 Imagen 生成)

「goen」與「ご縁」的文字排版,直接在設計軟體(Figma/Illustrator)或最終的網頁 CSS 裡用真實字體處理,**不要透過圖像生成**:

- **主要文字「goen」**:全小寫,幾何無襯線字體(建議 Inter 或 Geist),字重中等到偏粗(Medium–Semibold)
- **輔助文字「ご縁」**:字級約主文字的 40–50%,置於主文字下方或右側,字重較細(Regular),顏色可比主文字淡一階,做出類似 furigana 標音的視覺層次
- **與圖形標誌的組合(lockup)方式**:
  - 主要版本:icon 在左、「goen / ご縁」文字組在右,水平排列,兩者之間留適當間距(建議間距約 icon 寬度的 40–60%)
  - 窄版空間(手機版 header、favicon):只用圖形標誌本身,不帶文字
  - 直式版本(如需要):icon 在上、文字組在下,置中對齊,提供給需要方形/直式版位的場合(App icon、社群大頭貼等)使用
