package i18n

var (
	KeyShippingTitle = key("shipping.title", Message{ZhHant: "配送說明", En: "Delivery"})

	KeyShippingDescription = key("shipping.description", Message{
		ZhHant: "goen 的配送方式、運費與免運門檻。",
		En:     "Delivery methods, charges and the free-delivery threshold.",
	})

	KeyShippingSub = key("shipping.sub", Message{
		ZhHant: "以下金額直接來自系統實際計費的設定。",
		En:     "These figures come straight from what the checkout actually charges.",
	})

	KeyShippingMethods = key("shipping.methods", Message{
		ZhHant: "配送方式與運費",
		En:     "Methods and charges",
	})

	KeyShippingNoMethods = key("shipping.nomethods", Message{
		ZhHant: "目前沒有可用的配送方式。",
		En:     "No delivery methods are available at the moment.",
	})

	KeyShippingFreeOver = key("shipping.freeover", Message{ZhHant: "滿 %s 免運", En: "Free over %s"})

	KeyShippingZoneNote = key("shipping.zonenote", Message{
		ZhHant: "%s（免運不含）",
		En:     "%s (not covered by free delivery)",
	})

	// KeyFreeShippingExceptZones names the zones a free-delivery claim leaves out; %s is their names.
	KeyFreeShippingExceptZones = key("shipping.freeexcept", Message{
		ZhHant: "免運（%s另計）",
		En:     "Free (%s extra)",
	})

	KeyShippingZoneSurcharge = key("shipping.zonesurcharge", Message{
		ZhHant: "%s 另加 %s",
		En:     "%s: %s extra",
	})

	KeyListSeparator = key("common.listseparator", Message{
		ZhHant: "、",
		En:     ", ",
	})

	KeyListLastSeparator = key("common.listseparator.last", Message{
		ZhHant: "、",
		En:     " and ",
	})

	KeyShippingTracking = key("shipping.tracking", Message{ZhHant: "出貨與追蹤", En: "Dispatch and tracking"})

	KeyShippingHold = key("shipping.hold", Message{ZhHant: "庫存保留", En: "Stock reservation"})

	KeyShippingHoldBody = key("shipping.hold.body", Message{ZhHant: "送出訂單時系統會保留庫存 %s 分鐘，請在下單後 %s 分鐘內開始付款。保留時間結束仍未付款的訂單會自動取消，商品回到架上供其他人購買。", En: "Placing an order holds the stock for %s minutes; start the payment within %s minutes of ordering. An order still unpaid when the hold ends is cancelled automatically, and the goods go back on the shelf for somebody else."})

	KeyShippingTrackingBody = key("shipping.tracking.body", Message{
		ZhHant: "付款完成後我們會開始備貨。出貨時會記錄物流商與查詢編號，你可以在訂單頁看到，系統也會寄信通知。",
		En: "We start packing once the payment clears. When it ships we record the carrier " +
			"and the tracking number: both appear on your order page, and an email goes out.",
	})
)

var (
	KeyAdminNone = key("admin.none", Message{ZhHant: "無", En: "None"})

	KeyFormMethodCode = key("form.method.code", Message{
		ZhHant: "代碼只能用小寫英數與底線，例如 express_delivery。",
		En:     "A code takes lower-case letters, digits and underscores — express_delivery, for example.",
	})

	KeyFormMethodDestination = key("form.method.destination", Message{
		ZhHant: "請選擇送到地址或送到門市。",
		En:     "Choose whether this delivers to an address or to a pickup store.",
	})

	KeyFormMethodFee = key("form.method.fee", Message{
		ZhHant: "運費請填 0 到 %s 之間的整數金額。",
		En:     "Enter a whole-dollar delivery fee from 0 to %s.",
	})

	KeyFormMethodFreeOver = key("form.method.freeover", Message{
		ZhHant: "免運門檻請填 0 到 %s 之間的整數金額，或留空表示不免運。",
		En:     "Enter a whole-dollar free-delivery threshold from 0 to %s, or leave it blank for no free delivery.",
	})

	KeyFormShippingSurcharge = key("form.shipping.surcharge", Message{
		ZhHant: "分區加價請填 0 到 %s 之間的整數金額，或留空表示不加價。",
		En:     "Enter a whole-dollar zone surcharge from 0 to %s, or leave it blank for no surcharge.",
	})

	KeyFormMethodCodeTaken = key("form.method.code.taken", Message{
		ZhHant: "這個代碼已經有配送方式用了。",
		En:     "Another delivery method already uses that code.",
	})

	KeyFormMethodParcelLimit = key("form.method.parcel.limit", Message{
		ZhHant: "上限請填 1 到 %d 的整數，或留空或填 0 表示不設限。",
		En:     "A limit is a whole number from 1 to %d, or blank or 0 for no stated limit.",
	})

	KeyFormZoneCode = key("form.zone.code", Message{
		ZhHant: "代碼只能用小寫英數與底線，例如 mountain。",
		En:     "A code takes lower-case letters, digits and underscores — mountain, for example.",
	})

	KeyFormZoneCodeTaken = key("form.zone.code.taken", Message{
		ZhHant: "這個代碼已經有區域用了。",
		En:     "Another zone already uses that code.",
	})

	KeyFormZonePrefixRequired = key("form.zone.prefix.required", Message{
		ZhHant: "請至少填一個三位數郵遞區號前綴。",
		En:     "At least one three-digit postal-code prefix is required.",
	})

	KeyFormZonePrefixTooMany = key("form.zone.prefix.toomany", Message{
		ZhHant: "一次最多 100 個前綴。",
		En:     "100 prefixes at a time, at most.",
	})

	KeyFormZonePrefixShape = key("form.zone.prefix.shape", Message{
		ZhHant: "前綴必須是三位數字，例如 880。看到的是「%s」。",
		En:     "A prefix is three digits — 880, for example. This one reads %q.",
	})

	KeyAdminPageShipping = key("admin.page.shipping", Message{ZhHant: "配送與運費", En: "Delivery and fees"})

	KeyAdminShipLead = key("admin.ship.lead", Message{
		ZhHant: "修改運費會發布新版本，過去的訂單仍依下單時的版本計價。",
		En:     "Changing a fee publishes a new version; past orders keep the version they were priced from.",
	})

	KeyAdminShipCurrent = key("admin.ship.current", Message{
		ZhHant: "目前：%s，滿 %s 免運。",
		En:     "Currently %s, free delivery over %s.",
	})

	KeyAdminShipSince = countKey("admin.ship.since", "%s 起生效，共 %d 個版本。",
		"In force since %s, %d version so far.", "In force since %s, %d versions so far.")

	KeyAdminShipCarrierOptional = key("admin.ship.carrier.optional", Message{
		ZhHant: "物流商（選填）",
		En:     "Carrier (optional)",
	})

	KeyAdminShipCarrierEn = key("admin.ship.carrier.en", Message{
		ZhHant: "物流商（英文）",
		En:     "Carrier (English)",
	})

	KeyAdminShipFee = key("admin.ship.fee", Message{
		ZhHant: "運費（元）",
		En:     "Delivery fee (NT$)",
	})

	KeyAdminShipFreeOver = key("admin.ship.freeover", Message{
		ZhHant: "免運門檻（元，留空為無）",
		En:     "Free-delivery threshold (NT$, blank for none)",
	})

	KeyAdminShipPublish = key("admin.ship.publish", Message{
		ZhHant: "發布新版本",
		En:     "Publish a new version",
	})

	KeyAdminShipDisable = key("admin.ship.disable", Message{
		ZhHant: "停用這個方式",
		En:     "Switch this method off",
	})

	KeyAdminShipReenable = key("admin.ship.reenable", Message{
		ZhHant: "重新啟用",
		En:     "Switch it back on",
	})

	KeyAdminShipSurcharges = key("admin.ship.surcharges", Message{
		ZhHant: "分區加價",
		En:     "Zone surcharges",
	})

	KeyAdminShipSurchargeLead = key("admin.ship.surcharge.lead", Message{
		ZhHant: "免運只免基本運費，分區加價照收。填 0 即取消加價。",
		En:     "Free delivery covers the base fee only; a zone surcharge still applies. Enter 0 to remove a surcharge.",
	})

	KeyAdminShipPrefixCount = countKey("admin.ship.prefixcount", "%d 個郵遞區號",
		"%d postal code", "%d postal codes")

	KeyAdminShipSet = key("admin.ship.set", Message{ZhHant: "設定", En: "Set"})

	KeyAdminShipRefused = key("admin.ship.refused", Message{ZhHant: "配送設定未儲存，這項變更不符合配送規則。", En: "The shipping settings were not saved; this change breaks a delivery rule."})

	KeyAdminShipZoneHasPrefixes = key("admin.ship.zone.hasprefixes", Message{
		ZhHant: "這個分區還有郵遞區號，先移除它的郵遞區號再刪。",
		En:     "This zone still has postal-code prefixes. Remove its prefixes before deleting it.",
	})

	KeyAdminShipZoneHasVersions = key("admin.ship.zone.hasversions", Message{
		ZhHant: "配送方式的版本仍使用這個分區；發布新版本不會移除舊版本的加價紀錄，因此目前無法刪除這個分區。",
		En:     "Delivery-method versions still use this zone. Publishing a new version does not remove old surcharge records, so this zone cannot be deleted while those records remain.",
	})

	KeyAdminShipVersionChanged = key("admin.ship.version.changed", Message{
		ZhHant: "配送費率已變更。請確認目前設定，再重新送出分區加價。",
		En:     "Shipping fees changed. Review the current settings, then submit the zone surcharge again.",
	})

	KeyAdminShipNoZone = key("admin.ship.nozone", Message{
		ZhHant: "這個方式收件到門市，沒有郵遞區號，所以永遠不會落在任何分區裡。",
		En: "This method delivers to a store, so it has no postal code and can never fall inside " +
			"any zone.",
	})

	KeyAdminShipAddMethod = key("admin.ship.addmethod", Message{
		ZhHant: "新增配送方式",
		En:     "Add a delivery method",
	})

	KeyAdminShipCodeHint = key("admin.ship.code.hint", Message{
		ZhHant: "結帳的網址會帶這個代碼，之後不能改。",
		En:     "The checkout URL carries this code, and it cannot be changed afterwards.",
	})

	KeyAdminShipDestination = key("admin.ship.destination", Message{
		ZhHant: "送到哪裡",
		En:     "Where it delivers",
	})

	KeyAdminShipToAddress = key("admin.ship.toaddress", Message{
		ZhHant: "收件地址",
		En:     "Delivery address",
	})

	KeyAdminShipDestinationHint = key("admin.ship.destination.hint", Message{
		ZhHant: "這一項決定結帳要問街道還是問門市，選錯了收不到貨。",
		En: "This decides whether the checkout asks for a street or for a store. Choose the wrong " +
			"one and the parcel cannot be delivered.",
	})

	KeyAdminShipMaxLongest = key("admin.ship.maxlongest", Message{
		ZhHant: "包裹上限：最長邊（mm）",
		En:     "Parcel limit: longest side (mm)",
	})

	KeyAdminShipMaxSum = key("admin.ship.maxsum", Message{
		ZhHant: "包裹上限：三邊合（mm）",
		En:     "Parcel limit: three sides added (mm)",
	})

	KeyAdminShipMaxWeight = key("admin.ship.maxweight", Message{
		ZhHant: "包裹上限：重量（g）",
		En:     "Parcel limit: weight (g)",
	})

	KeyAdminShipLimitsHint = key("admin.ship.limits.hint", Message{
		ZhHant: "空白代表沒有上限。超商店到店是 450 / 1050 / 10000，萊爾富重量只到 5000。",
		En: "Blank means no limit. Convenience-store counter-to-counter is 450 / 1050 / 10000, " +
			"and Hi-Life takes only 5000 by weight.",
	})

	KeyAdminShipFirstVersion = key("admin.ship.firstversion", Message{
		ZhHant: "新增方式時會一起發布第一個版本。",
		En:     "Adding a method publishes its first version with it.",
	})

	KeyAdminShipZones = key("admin.ship.zones", Message{ZhHant: "分區", En: "Zones"})

	KeyAdminShipColZone = key("admin.ship.col.zone", Message{ZhHant: "分區", En: "Zone"})

	KeyAdminShipPrefixes = key("admin.ship.prefixes", Message{
		ZhHant: "郵遞區號前三碼",
		En:     "First three digits of the postal code",
	})

	KeyAdminShipZonePrefixes = key("admin.ship.zone.prefixes", Message{
		ZhHant: "%s 的郵遞區號",
		En:     "Postal codes for %s",
	})

	KeyAdminShipAddZone = key("admin.ship.addzone", Message{ZhHant: "新增分區", En: "Add a zone"})

	KeyAdminShipZoneNameEnHint = key("admin.ship.zone.nameen.hint", Message{
		ZhHant: "/shipping 和結帳的加價說明都會讀這個名字。",
		En:     "/shipping and the surcharge sentence at checkout both read this name.",
	})

	KeyAdminShipPrefixesHint = key("admin.ship.prefixes.hint", Message{
		ZhHant: "空白或逗號分隔。新增分區時至少填一個；之後可把上方欄位清空，讓分區不再命中任何郵遞區號。",
		En: "Separated by spaces or commas. A new zone needs at least one; clear its field later " +
			"to make it match no postal code.",
	})
)
