package i18n

// /admin/shipping — the delivery methods, their versions and the zone surcharges.
//
// The page's whole argument is that a fee is never edited: changing one PUBLISHES
// a new version, because every past order names the version it was priced from.
// That is the sentence under the heading and it is translated in full — a staff
// member who reads "Publish" as a synonym for "Save" will expect the old figure
// to be gone, and it is not.
//
// Two pieces of vocabulary this page needs and the shared file does not carry:
//
//   - 分區 is a SECTION heading over the list and a COLUMN heading over one row,
//     which English pulls apart into "Zones" and "Zone" exactly as it pulls
//     訂單 apart. The Chinese cannot show the difference, so this is one of the
//     places where the split has to be made deliberately rather than noticed.
//   - 超商取貨 is convenience-store pickup, and the destination it collects is a
//     STORE rather than an address. Which half of the checkout exists follows
//     from that choice, which is why the form asks it instead of deriving it
//     from the code.

var (
	// The heading's lead. "New version" rather than "save" is the load-bearing
	// half: a form that silently appends looks broken to somebody expecting to
	// edit, and an edit that rewrote the old figure would rewrite what a
	// customer was charged last month.
	KeyAdminShipLead = key("admin.ship.lead", Message{
		ZhHant: "改運費是「發布新版本」,不是改舊的 —— 每一張過去的訂單都記著自己是用哪個版本計價的。",
		En: "Changing a fee publishes a NEW version rather than editing the old one — every past " +
			"order records which version it was priced from.",
	})

	// The two sentences under a method's name. Each is one message with holes
	// rather than a label glued to a figure: 滿 %s 免運 and "free delivery over
	// %s" put the number in different places, and concatenation can only serve
	// one of them.
	KeyAdminShipCurrent = key("admin.ship.current", Message{
		ZhHant: "目前:%s,滿 %s 免運。",
		En:     "Currently %s, free delivery over %s.",
	})
	KeyAdminShipSince = key("admin.ship.since", Message{
		ZhHant: "%s 起生效,共 %s 個版本。",
		En:     "In force since %s, %s versions in all.",
	})

	// The version form. 名稱, 名稱(英文) and 物流商 are the shared columns; these
	// three are what this form says differently — the carrier is marked optional
	// here and not on the create form, and the two money fields state their unit
	// because a form that asks for cents is a form that eventually charges a
	// hundred times too much.
	KeyAdminShipCarrierOptional = key("admin.ship.carrier.optional", Message{
		ZhHant: "物流商(選填)",
		En:     "Carrier (optional)",
	})
	KeyAdminShipCarrierEn = key("admin.ship.carrier.en", Message{
		ZhHant: "物流商(英文)",
		En:     "Carrier (English)",
	})
	KeyAdminShipFee = key("admin.ship.fee", Message{
		ZhHant: "運費(元)",
		En:     "Delivery fee (NT$)",
	})
	// Blank is a real answer and the label says so: zero means the fee always
	// applies, and an empty box is how "there is no threshold" is written.
	KeyAdminShipFreeOver = key("admin.ship.freeover", Message{
		ZhHant: "免運門檻(元,留空為無)",
		En:     "Free-delivery threshold (NT$, blank for none)",
	})
	KeyAdminShipPublish = key("admin.ship.publish", Message{
		ZhHant: "發布新版本",
		En:     "Publish a new version",
	})

	// Off, never deleted — so the control names the ACT in both directions and
	// neither of them is "delete".
	KeyAdminShipDisable = key("admin.ship.disable", Message{
		ZhHant: "停用這個方式",
		En:     "Switch this method off",
	})
	KeyAdminShipReenable = key("admin.ship.reenable", Message{
		ZhHant: "重新啟用",
		En:     "Switch it back on",
	})
)

var (
	// The zone surcharges on one version.
	KeyAdminShipSurcharges = key("admin.ship.surcharges", Message{
		ZhHant: "分區加價",
		En:     "Zone surcharges",
	})
	// Which side of the free-delivery threshold the surcharge falls on is a
	// COMMERCIAL decision, not arithmetic, so the reason travels with it: the
	// shop is waiving its own base rate and the carrier is still charging to
	// cross the water.
	KeyAdminShipSurchargeLead = key("admin.ship.surcharge.lead", Message{
		ZhHant: "加在免運之後 —— 免運是本店對自己基本運費的優惠,跨海的錢是物流商收的。填 0 就是取消加價。",
		En: "Added AFTER the free-delivery threshold — free delivery is this shop's own offer on its " +
			"own base rate, and the carrier still charges to cross the water. Enter 0 to clear a surcharge.",
	})
	KeyAdminShipPrefixCount = key("admin.ship.prefixcount", Message{
		ZhHant: "%s 個郵遞區號",
		En:     "%s postal codes",
	})
	// One word for two small inline forms — the surcharge and a zone's prefixes.
	// Both write one figure back where it already sits, which is what makes
	// "Set" right and "Add" wrong.
	KeyAdminShipSet = key("admin.ship.set", Message{ZhHant: "設定", En: "Set"})
	// A pickup method is never matched to a zone, so the form is absent and this
	// says why rather than leaving a section that looks unfinished.
	KeyAdminShipNoZone = key("admin.ship.nozone", Message{
		ZhHant: "這個方式收件到門市,沒有郵遞區號,所以永遠不會落在任何分區裡。",
		En: "This method delivers to a store, so it has no postal code and can never fall inside " +
			"any zone.",
	})
)

var (
	// Adding a method. The heading and the submit button say the same words on
	// purpose — the section IS the act — so they are one declaration.
	KeyAdminShipAddMethod = key("admin.ship.addmethod", Message{
		ZhHant: "新增配送方式",
		En:     "Add a delivery method",
	})
	KeyAdminShipCodeHint = key("admin.ship.code.hint", Message{
		ZhHant: "結帳的網址會帶這個代碼,之後不能改。",
		En:     "The checkout URL carries this code, and it cannot be changed afterwards.",
	})
	KeyAdminShipDestination = key("admin.ship.destination", Message{
		ZhHant: "送到哪裡",
		En:     "Where it delivers",
	})
	// The option reads 收件地址 where a method's badge reads 宅配地址, so this is
	// its own key rather than KeyAdminDestAddress: translating a surface is not
	// the moment to make two strings agree. The pickup half IS the same string
	// and reuses KeyAdminDestPickup.
	KeyAdminShipToAddress = key("admin.ship.toaddress", Message{
		ZhHant: "收件地址",
		En:     "Delivery address",
	})
	// destination_kind decides which half of the checkout exists. The refusal it
	// produces is silent — the customer types the only fields on screen — so the
	// consequence is stated at the moment of choosing.
	KeyAdminShipDestinationHint = key("admin.ship.destination.hint", Message{
		ZhHant: "這一項決定結帳要問街道還是問門市,選錯了收不到貨。",
		En: "This decides whether the checkout asks for a street or for a store. Choose the wrong " +
			"one and the parcel cannot be delivered.",
	})

	// What the carrier will physically take, brought forward to the checkout so
	// a counter refusal happens before the parcel is packed.
	KeyAdminShipMaxLongest = key("admin.ship.maxlongest", Message{
		ZhHant: "包裹上限:最長邊(mm)",
		En:     "Parcel limit: longest side (mm)",
	})
	KeyAdminShipMaxSum = key("admin.ship.maxsum", Message{
		ZhHant: "包裹上限:三邊合(mm)",
		En:     "Parcel limit: three sides added (mm)",
	})
	KeyAdminShipMaxWeight = key("admin.ship.maxweight", Message{
		ZhHant: "包裹上限:重量(g)",
		En:     "Parcel limit: weight (g)",
	})
	// 萊爾富 is Hi-Life, which is the chain's own English name rather than a
	// transliteration — an English reader has to be able to recognise the shop
	// on the sign. The figures are carried across unchanged: they are what the
	// carriers publish, and rounding them off in one locale would make the two
	// pages disagree about what fits.
	KeyAdminShipLimitsHint = key("admin.ship.limits.hint", Message{
		ZhHant: "空白代表沒有上限。超商店到店是 450 / 1050 / 10000,萊爾富重量只到 5000。",
		En: "Blank means no limit. Convenience-store counter-to-counter is 450 / 1050 / 10000, " +
			"and Hi-Life takes only 5000 by weight.",
	})

	// An example of the CANONICAL name, beside a separate English field whose
	// own placeholder is already English. Both are translated rather than left
	// as written: a Han placeholder is a Han string on an English page whichever
	// field it demonstrates.
	KeyAdminShipNamePlaceholder = key("admin.ship.name.placeholder", Message{
		ZhHant: "隔日到貨",
		En:     "Next-day delivery",
	})
	// A method with no version is one the checkout FINDS and cannot price, and
	// shipping_method_versions is append-only, so there is no repairing that by
	// editing afterwards. Hence both in one transaction, and hence the sentence.
	KeyAdminShipFirstVersion = key("admin.ship.firstversion", Message{
		ZhHant: "新增方式時會一起發布第一個版本 —— 沒有版本的方式結帳找得到卻算不出運費。",
		En: "Adding a method publishes its first version with it — a method with no version is one " +
			"the checkout finds and cannot price.",
	})
)

var (
	// 分區 twice, and the two are not one key. The section heading names the
	// whole list and the column heading names one row; English says "Zones" and
	// "Zone" and a single key could only be right about one of them.
	KeyAdminShipZones   = key("admin.ship.zones", Message{ZhHant: "分區", En: "Zones"})
	KeyAdminShipColZone = key("admin.ship.col.zone", Message{ZhHant: "分區", En: "Zone"})
	// The bound is in the words: a prefix is the first THREE digits, which is
	// what shipping_zone_prefixes stores and what the refusal quotes back.
	KeyAdminShipPrefixes = key("admin.ship.prefixes", Message{
		ZhHant: "郵遞區號前三碼",
		En:     "First three digits of the postal code",
	})
	// Screen-reader only: every zone's prefix box is a separate control with the
	// same visible heading above it, so the accessible name has to name the zone.
	KeyAdminShipZonePrefixes = key("admin.ship.zone.prefixes", Message{
		ZhHant: "%s 的郵遞區號",
		En:     "Postal codes for %s",
	})

	KeyAdminShipAddZone             = key("admin.ship.addzone", Message{ZhHant: "新增分區", En: "Add a zone"})
	KeyAdminShipZoneNamePlaceholder = key("admin.ship.zone.name.placeholder", Message{
		ZhHant: "離島",
		En:     "Outlying islands",
	})
	// Says WHERE the English name is read, because that is what makes filling it
	// in worth the staff member's minute: 離島 appears inside the surcharge
	// sentence a customer is shown before being charged it.
	KeyAdminShipZoneNameEnHint = key("admin.ship.zone.nameen.hint", Message{
		ZhHant: "/shipping 和結帳的加價說明都會讀這個名字。",
		En:     "/shipping and the surcharge sentence at checkout both read this name.",
	})
	// A zone is found BY prefix, so an empty one is a row nothing can reach —
	// stated as the consequence rather than as a rule, because the form accepts
	// it and the surcharge simply never fires.
	KeyAdminShipPrefixesHint = key("admin.ship.prefixes.hint", Message{
		ZhHant: "空白或逗號分隔。沒有前綴的分區永遠不會被任何郵遞區號命中,也就永遠不會加價。",
		En: "Separated by spaces or commas. A zone with no prefixes is matched by no postal code, " +
			"so it can never add a surcharge.",
	})
)
