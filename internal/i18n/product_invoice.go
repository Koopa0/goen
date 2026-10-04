package i18n

var (
	KeyProductInvoiceLine           = key("product.invoice.title", Message{ZhHant: "發票資訊", En: "Invoice details"})
	KeyInvoiceTaxType           = key("product.invoice.tax", Message{ZhHant: "課稅別", En: "Tax type"})
	KeyInvoiceTaxable           = key("invoice.tax.taxable", Message{ZhHant: "應稅", En: "Taxable"})
	KeyInvoiceExempt            = key("invoice.tax.exempt", Message{ZhHant: "免稅", En: "Exempt"})
	KeyInvoiceZeroRated         = key("invoice.tax.zero_rated", Message{ZhHant: "零稅率", En: "Zero-rated"})
	KeyInvoiceUnit              = key("product.invoice.unit", Message{ZhHant: "發票單位", En: "Invoice unit"})
	KeyInvoiceUnitHint          = key("product.invoice.unit.hint", Message{ZhHant: "以中文填寫，最多六個字。", En: "Use a Chinese invoice unit, up to six characters."})
	KeyInvoiceProductTaxInvalid = key("product.invoice.tax.invalid", Message{ZhHant: "商品課稅別請選擇應稅或免稅。", En: "Choose taxable or exempt for a product."})
	KeyInvoiceUnitInvalid       = key("product.invoice.unit.invalid", Message{ZhHant: "請填寫不含控制字元且不超過六個字的發票單位。", En: "Enter an invoice unit of up to six characters without control characters."})
	KeyInvoiceLineSave         = key("product.invoice.save", Message{ZhHant: "儲存發票資訊", En: "Save invoice details"})
	KeyAuditProductInvoiceLine      = key("audit.product.invoice", Message{ZhHant: "修改商品發票資訊", En: "Edit product invoice details"})
)
