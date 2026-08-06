# goen 的完整度路線

目標:一個可以拿來展示的完整電商 —— 功能豐富度對得起 Amazon / 蝦皮 / 主流
open-source 電商,軟體工程與系統設計本身也是展示的一部分。視覺**不參考**任何
既有電商,goen 有自己的設計語言(`docs/decisions/002-storefront-design-approach.md`)。

量測基準(2026-07-27):52 條路由、54 張表、15,057 行 Go、9,521 行測試。

## 分類的原則

排序不是照「功能清單的長度」,是照三個問題:

1. **看得到但用不了的**,優先於還沒有的 —— 那是缺陷,不是缺功能。
2. **購買主線上的**,優先於周邊 —— 一個不能用優惠券的電商不完整。
3. **schema 已經設計好的**,優先於要新設計的 —— 那代表當初想過,只是沒蓋。

---

## A. 看得到但用不了(缺陷)

| 項目 | 現況 | 缺什麼 |
|---|---|---|
| 商品評價 | 已完成:顧客留評價、`product_reviews_verified_is_real` 守住已購買徽章、後台在 `/admin/reviews` 隱藏(同時從評分裡拿掉) | — |
| 發票開立 | 收集了偏好,`invoice_documents` 空著 | 加值中心整合(阻塞於第三方) |

忘記密碼已補上(`/forgot`、`/reset`)。在那之前 `password_reset_tokens` 有表、
有三條 query、沒有任何路由 —— argon2 沒有人查得回來,所以忘記密碼等於永久鎖死。

## B. 購買主線(表需要新設計)

| 項目 | 為什麼是主線 |
|---|---|
| **優惠券 / 折扣碼** | `orders.discount_cents` 永遠是 0。滿額折、首購、單品折、免運碼 |
| **限時特價活動** | `sale_campaigns` 已存在但沒有程式碼;`/deals` 目前只看變體折扣 |
| **會員等級 / 點數** | 已完成:點數是 `loyalty_entries` 帳本,等級是 `membership_tiers`,由近一年**已成立訂單**的消費金額推導,後台在 `/admin/tiers` 管理 |
| **運費規則** | 離島加價已完成(`shipping_zones` + `shipping_version_zones`,結帳會重新報價並要求確認)。重量與材積還沒有。超商 vs 宅配是兩種**收件方式**(`shipping_methods.destination_kind`),不只是兩種費率 |

## C. 後台(開店本身)

| 項目 | 現況 |
|---|---|
| **商品管理** | 只能改既有商品的庫存/價格/上下架 —— **無法新增商品**,目錄只能靠 seed |
| 分類 / 品牌管理 | 已完成(`/admin/taxonomy`) |
| 運費管理 | 已完成(`/admin/shipping`):發布新版本、分區加價 |
| 商品圖片上傳 | 無(媒體管線不存在,目前是佔位圖) |
| 首頁內容管理 | `hero_slides`、`promo_banners` 有表沒程式碼 |
| 報表 / 分析 | 無。營收、熱銷、轉換、庫存週轉 |
| 員工帳號管理 | 無。角色、權限、`staff_totp_credentials` 兩階段驗證 |
| 稽核軌跡 | `audit_events` 有表沒程式碼 |

## D. 顧客體驗

| 項目 | 參考來源 |
|---|---|
| 補貨通知 | 已完成:PDP 留信箱、補貨時經 outbox 寄出 |
| 商品問答 (Q&A) | 蝦皮/Amazon 都有,表也沒有 |
| 推薦(買了又買 / 看了又看) | 需要瀏覽與共購資料 |
| 商品比較 | 3C 選品店特別需要 |
| 保固登錄 | `warranty_registrations` 有表沒程式碼 |
| 通知中心 | 無(顧客端)。店家端的聯絡訊息收件匣已完成:`/admin/messages` |
| 社群登入 | `user_identities` 有表沒程式碼 |

## E. 系統設計(展示面)

| 項目 | 現況 |
|---|---|
| **Outbox** | `outbox_messages` 有表沒程式碼。訂單事件 → email/通知,與寫入同交易 |
| **搜尋投影** | `product_search_documents` 有表沒程式碼。中文搜尋目前是 seq scan(已量測:10,000 商品時 8.8ms) |
| **Email 實際寄送** | 五個主題都有生產者與處理器,`TestEveryTopicHasAProducerAndAHandler` 守住 |
| 觀測性 | 無 tracing/metrics |
| 快取 | 無 |
| 速率限制 | 無。登入端點特別需要 |
| 讀模型投影 | 首頁排名目前每次計算(已量測:10,000 商品時 60ms) |

## F. 站台品質

| 項目 | 現況 |
|---|---|
| SEO | 無 `sitemap.xml`、無 `robots.txt`、無 JSON-LD |
| 多國語系 | 已完成:`internal/i18n`,英文是真的 locale。**還缺**:句子型的 chrome(頁首說明、表單下方的指示)仍是寫死的中文,只有控制項與標籤跟著語系 |
| 政策頁 | 七頁 404,內容需要商業決定 |

---

## 有 query 沒有呼叫者(2026-07-28 稽核,已清空)

`internal/db` 產生 231 個方法,稽核當下有 14 個在 `internal/db` 之外沒有任何
呼叫者。每一個都寫了註解說明用途 —— 不是死碼,是**蓋到一半的功能**,而且在
review 裡看起來像蓋好了。

十二個接上,兩個刪掉:

| 接上的 | 原本缺什麼 |
|---|---|
| `SetDefaultAddress` / `ClearDefaultAddress` | 顧客改不了預設地址,而結帳就是從預設地址帶入的 |
| `WishlistHas` | 商品頁不知道有沒有收藏過 |
| `CancelPayment` | `checkout.session.expired` 沒有處理,放棄的 session 永遠停在 `requires_payment` |
| `DeleteExpiredSessions` | 沒有人清過期 session |
| `UnreferencedMedia` / `DeleteMedia` | 沒接上的上傳永遠不回收 |
| `RefundedSoFar` | 超額退款只有 constraint 名字會講話 |
| `ReturnLines` | 後台看不到退貨裡有什麼就要決定 |
| `CreditBalance` | 發放購物金看不到餘額,重複發放的來源 |
| `OrderEvents` / `OrderShipments` | 後台訂單頁沒有時間軸與出貨紀錄(顧客端有) |
| `StaffTOTPStatus` | 沒有「誰開了兩階段驗證」的頁面 |
| `UserByID` | 個人資料表單的電話欄位每次都是空的 |

刪掉的:`ProductRatingSummary`(與 `ProductRating` 完全重複,只是改用 slug)。

`TestEveryGeneratedQueryHasACaller` 從此守住,**沒有例外清單** —— 例外清單會長大,
而這裡的失敗模式就是「看起來寫完了」。

## 不做的

- **多商家 / marketplace**:goen 是選品店,那是另一個產品。
- **抄任何既有電商的視覺**:功能可以參考,畫面不行。
