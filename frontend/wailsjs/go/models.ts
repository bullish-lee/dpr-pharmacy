export namespace applog {
	
	export class Line {
	    time: string;
	    level: string;
	    msg: string;
	    attrs: string;
	    raw: string;
	
	    static createFrom(source: any = {}) {
	        return new Line(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.level = source["level"];
	        this.msg = source["msg"];
	        this.attrs = source["attrs"];
	        this.raw = source["raw"];
	    }
	}

}

export namespace crawler {
	
	export class StdSnapshot {
	    uddi: string;
	    date: string;
	    title: string;
	
	    static createFrom(source: any = {}) {
	        return new StdSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.uddi = source["uddi"];
	        this.date = source["date"];
	        this.title = source["title"];
	    }
	}

}

export namespace domain {
	
	export class CompRule {
	    rateBp: number;
	    windowMode: string;
	    lookback: number;
	    cutoffDay: number;
	    extendToAnchor: boolean;
	    anchor: string;
	    rounding: string;
	    netReturns: boolean;
	    settlementDay: number;
	
	    static createFrom(source: any = {}) {
	        return new CompRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rateBp = source["rateBp"];
	        this.windowMode = source["windowMode"];
	        this.lookback = source["lookback"];
	        this.cutoffDay = source["cutoffDay"];
	        this.extendToAnchor = source["extendToAnchor"];
	        this.anchor = source["anchor"];
	        this.rounding = source["rounding"];
	        this.netReturns = source["netReturns"];
	        this.settlementDay = source["settlementDay"];
	    }
	}
	export class GS1 {
	    gtin: string;
	    expiry: string;
	    lot: string;
	    serial: string;
	    raw: string;
	    elements: Record<string, string>;
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new GS1(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.gtin = source["gtin"];
	        this.expiry = source["expiry"];
	        this.lot = source["lot"];
	        this.serial = source["serial"];
	        this.raw = source["raw"];
	        this.elements = source["elements"];
	        this.warnings = source["warnings"];
	    }
	}
	export class SupplierPosition {
	    supplierId: number;
	    supplierName: string;
	    rule?: CompRule;
	    ruleText: string;
	    windowFrom: string;
	    windowTo: string;
	    purchased: number;
	    returned: number;
	    estimatedHeld: number;
	    scannedHeld?: number;
	    docQty: number;
	    heldUsed: number;
	    method: string;
	    recoverQty: number;
	    recovery: number;
	
	    static createFrom(source: any = {}) {
	        return new SupplierPosition(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.supplierId = source["supplierId"];
	        this.supplierName = source["supplierName"];
	        this.rule = this.convertValues(source["rule"], CompRule);
	        this.ruleText = source["ruleText"];
	        this.windowFrom = source["windowFrom"];
	        this.windowTo = source["windowTo"];
	        this.purchased = source["purchased"];
	        this.returned = source["returned"];
	        this.estimatedHeld = source["estimatedHeld"];
	        this.scannedHeld = source["scannedHeld"];
	        this.docQty = source["docQty"];
	        this.heldUsed = source["heldUsed"];
	        this.method = source["method"];
	        this.recoverQty = source["recoverQty"];
	        this.recovery = source["recovery"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProductDecision {
	    stock: number;
	    delta: number;
	    grossLoss: number;
	    totalDocQty: number;
	    recommendation: string;
	    positions: SupplierPosition[];
	    recoverQty: number;
	    recovery: number;
	    netLoss: number;
	    scanned: boolean;
	    physicalUpside: number;
	    upsideAmount: number;
	
	    static createFrom(source: any = {}) {
	        return new ProductDecision(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stock = source["stock"];
	        this.delta = source["delta"];
	        this.grossLoss = source["grossLoss"];
	        this.totalDocQty = source["totalDocQty"];
	        this.recommendation = source["recommendation"];
	        this.positions = this.convertValues(source["positions"], SupplierPosition);
	        this.recoverQty = source["recoverQty"];
	        this.recovery = source["recovery"];
	        this.netLoss = source["netLoss"];
	        this.scanned = source["scanned"];
	        this.physicalUpside = source["physicalUpside"];
	        this.upsideAmount = source["upsideAmount"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace ingest {
	
	export class Field {
	    key: string;
	    label: string;
	    synonyms: string[];
	    required: boolean;
	    help: string;
	
	    static createFrom(source: any = {}) {
	        return new Field(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.synonyms = source["synonyms"];
	        this.required = source["required"];
	        this.help = source["help"];
	    }
	}
	export class Override {
	    sheet: string;
	    headerRow: number;
	    mapping: Record<string, number>;
	
	    static createFrom(source: any = {}) {
	        return new Override(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sheet = source["sheet"];
	        this.headerRow = source["headerRow"];
	        this.mapping = source["mapping"];
	    }
	}
	export class Preview {
	    sheet: string;
	    headerRow: number;
	    headers: string[];
	    mapping: Record<string, number>;
	    missing: string[];
	    sample: string[][];
	    rowCount: number;
	    signature: string;
	
	    static createFrom(source: any = {}) {
	        return new Preview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sheet = source["sheet"];
	        this.headerRow = source["headerRow"];
	        this.headers = source["headers"];
	        this.mapping = source["mapping"];
	        this.missing = source["missing"];
	        this.sample = source["sample"];
	        this.rowCount = source["rowCount"];
	        this.signature = source["signature"];
	    }
	}
	export class RowIssue {
	    sheet: string;
	    row: number;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new RowIssue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sheet = source["sheet"];
	        this.row = source["row"];
	        this.message = source["message"];
	    }
	}
	export class Schema {
	    kind: string;
	    label: string;
	    fields: Field[];
	
	    static createFrom(source: any = {}) {
	        return new Schema(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.fields = this.convertValues(source["fields"], Field);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace main {
	
	export class AppInfo {
	    version: string;
	    dataDir: string;
	    isWindows: boolean;
	    startRoute: string;
	
	    static createFrom(source: any = {}) {
	        return new AppInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.dataDir = source["dataDir"];
	        this.isWindows = source["isWindows"];
	        this.startRoute = source["startRoute"];
	    }
	}
	export class ImportKind {
	    kind: string;
	    label: string;
	    help: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportKind(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.help = source["help"];
	    }
	}
	export class RulePreview {
	    description: string;
	    windowFrom: string;
	    windowTo: string;
	    settlementDate: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new RulePreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.description = source["description"];
	        this.windowFrom = source["windowFrom"];
	        this.windowTo = source["windowTo"];
	        this.settlementDate = source["settlementDate"];
	        this.error = source["error"];
	    }
	}

}

export namespace service {
	
	export class AnchorView {
	    id: number;
	    asOf: string;
	    timing: string;
	    qty: number;
	    source: string;
	    note: string;
	    createdAt: string;
	
	    static createFrom(source: any = {}) {
	        return new AnchorView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.asOf = source["asOf"];
	        this.timing = source["timing"];
	        this.qty = source["qty"];
	        this.source = source["source"];
	        this.note = source["note"];
	        this.createdAt = source["createdAt"];
	    }
	}
	export class BarcodeInfo {
	    gtin: string;
	    packQty: number;
	    perBarcode: number;
	    name: string;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new BarcodeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.gtin = source["gtin"];
	        this.packQty = source["packQty"];
	        this.perBarcode = source["perBarcode"];
	        this.name = source["name"];
	        this.source = source["source"];
	    }
	}
	export class BatchView {
	    id: number;
	    kind: string;
	    fileName: string;
	    rangeFrom: string;
	    rangeTo: string;
	    rows: number;
	    live: number;
	    createdAt: string;
	
	    static createFrom(source: any = {}) {
	        return new BatchView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.fileName = source["fileName"];
	        this.rangeFrom = source["rangeFrom"];
	        this.rangeTo = source["rangeTo"];
	        this.rows = source["rows"];
	        this.live = source["live"];
	        this.createdAt = source["createdAt"];
	    }
	}
	export class Candidate {
	    code: string;
	    name: string;
	    score: number;
	    unitPrice: number;
	    reason?: string;
	    source?: string;
	
	    static createFrom(source: any = {}) {
	        return new Candidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.name = source["name"];
	        this.score = source["score"];
	        this.unitPrice = source["unitPrice"];
	        this.reason = source["reason"];
	        this.source = source["source"];
	    }
	}
	export class ChangeEntry {
	    id: number;
	    at: string;
	    kind: string;
	    productCode: string;
	    productName: string;
	    oldValue: string;
	    newValue: string;
	    note: string;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new ChangeEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.at = source["at"];
	        this.kind = source["kind"];
	        this.productCode = source["productCode"];
	        this.productName = source["productName"];
	        this.oldValue = source["oldValue"];
	        this.newValue = source["newValue"];
	        this.note = source["note"];
	        this.source = source["source"];
	    }
	}
	export class ChangeFilter {
	    productCode: string;
	    kind: string;
	    query: string;
	    limit: number;
	
	    static createFrom(source: any = {}) {
	        return new ChangeFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.productCode = source["productCode"];
	        this.kind = source["kind"];
	        this.query = source["query"];
	        this.limit = source["limit"];
	    }
	}
	export class ChangeRow {
	    id: number;
	    noticeId: number;
	    code: string;
	    name: string;
	    efarmName: string;
	    company: string;
	    spec: string;
	    unit: string;
	    kind: string;
	    detail: string;
	    priceBefore: number;
	    priceAfter: number;
	    rate: number;
	    beforeSource: string;
	    effectiveDate: string;
	    graceUntil: string;
	    flexible: boolean;
	    category: string;
	    ingredient: string;
	    sheet: string;
	    note: string;
	    stock: number;
	    held: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ChangeRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.noticeId = source["noticeId"];
	        this.code = source["code"];
	        this.name = source["name"];
	        this.efarmName = source["efarmName"];
	        this.company = source["company"];
	        this.spec = source["spec"];
	        this.unit = source["unit"];
	        this.kind = source["kind"];
	        this.detail = source["detail"];
	        this.priceBefore = source["priceBefore"];
	        this.priceAfter = source["priceAfter"];
	        this.rate = source["rate"];
	        this.beforeSource = source["beforeSource"];
	        this.effectiveDate = source["effectiveDate"];
	        this.graceUntil = source["graceUntil"];
	        this.flexible = source["flexible"];
	        this.category = source["category"];
	        this.ingredient = source["ingredient"];
	        this.sheet = source["sheet"];
	        this.note = source["note"];
	        this.stock = source["stock"];
	        this.held = source["held"];
	    }
	}
	export class CountInput {
	    code: string;
	    qty: number;
	    unit: string;
	    asOf: string;
	    timing: string;
	    note: string;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new CountInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.qty = source["qty"];
	        this.unit = source["unit"];
	        this.asOf = source["asOf"];
	        this.timing = source["timing"];
	        this.note = source["note"];
	        this.source = source["source"];
	    }
	}
	export class CrawlResult {
	    checked: number;
	    matched: number;
	    new: number;
	    notices: string[];
	    errors: string[];
	    skipped: boolean;
	    deferred: number;
	
	    static createFrom(source: any = {}) {
	        return new CrawlResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.checked = source["checked"];
	        this.matched = source["matched"];
	        this.new = source["new"];
	        this.notices = source["notices"];
	        this.errors = source["errors"];
	        this.skipped = source["skipped"];
	        this.deferred = source["deferred"];
	    }
	}
	export class CrawlStatus {
	    enabled: boolean;
	    times: string;
	    lastRun: string;
	    lastStatus: string;
	    lastOk: string;
	    message: string;
	    stale: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CrawlStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.times = source["times"];
	        this.lastRun = source["lastRun"];
	        this.lastStatus = source["lastStatus"];
	        this.lastOk = source["lastOk"];
	        this.message = source["message"];
	        this.stale = source["stale"];
	    }
	}
	export class CutDate {
	    effectiveDate: string;
	    notices: string[];
	    down: number;
	    up: number;
	    new: number;
	    delete: number;
	    agreed: number;
	    heldDown: number;
	    heldDelete: number;
	    grossLoss: number;
	    recovery: number;
	    netLoss: number;
	    done: number;
	    isPast: boolean;
	    daysLeft: number;
	    computed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CutDate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.effectiveDate = source["effectiveDate"];
	        this.notices = source["notices"];
	        this.down = source["down"];
	        this.up = source["up"];
	        this.new = source["new"];
	        this.delete = source["delete"];
	        this.agreed = source["agreed"];
	        this.heldDown = source["heldDown"];
	        this.heldDelete = source["heldDelete"];
	        this.grossLoss = source["grossLoss"];
	        this.recovery = source["recovery"];
	        this.netLoss = source["netLoss"];
	        this.done = source["done"];
	        this.isPast = source["isPast"];
	        this.daysLeft = source["daysLeft"];
	        this.computed = source["computed"];
	    }
	}
	export class HealthItem {
	    key: string;
	    level: string;
	    title: string;
	    detail: string;
	    count: number;
	    examples: string[];
	    link: string;
	
	    static createFrom(source: any = {}) {
	        return new HealthItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.level = source["level"];
	        this.title = source["title"];
	        this.detail = source["detail"];
	        this.count = source["count"];
	        this.examples = source["examples"];
	        this.link = source["link"];
	    }
	}
	export class Pending {
	    unresolved: number;
	    ambiguousScans: number;
	    planned: number;
	    rulesToReview: number;
	    unread: number;
	
	    static createFrom(source: any = {}) {
	        return new Pending(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.unresolved = source["unresolved"];
	        this.ambiguousScans = source["ambiguousScans"];
	        this.planned = source["planned"];
	        this.rulesToReview = source["rulesToReview"];
	        this.unread = source["unread"];
	    }
	}
	export class NoticeView {
	    id: number;
	    source: string;
	    listNo: string;
	    noticeNo: string;
	    title: string;
	    url: string;
	    publishedAt: string;
	    status: string;
	    error: string;
	    fileName: string;
	    createdAt: string;
	    counts: Record<string, number>;
	    dates: string[];
	    warnings: string[];
	    heldDown: number;
	    heldDelete: number;
	    heldChange: number;
	
	    static createFrom(source: any = {}) {
	        return new NoticeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.source = source["source"];
	        this.listNo = source["listNo"];
	        this.noticeNo = source["noticeNo"];
	        this.title = source["title"];
	        this.url = source["url"];
	        this.publishedAt = source["publishedAt"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.fileName = source["fileName"];
	        this.createdAt = source["createdAt"];
	        this.counts = source["counts"];
	        this.dates = source["dates"];
	        this.warnings = source["warnings"];
	        this.heldDown = source["heldDown"];
	        this.heldDelete = source["heldDelete"];
	        this.heldChange = source["heldChange"];
	    }
	}
	export class DeletionRow {
	    code: string;
	    name: string;
	    noticeName: string;
	    company: string;
	    stock: number;
	    price: number;
	    value: number;
	    effectiveDate: string;
	    graceUntil: string;
	    noticeNo: string;
	
	    static createFrom(source: any = {}) {
	        return new DeletionRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.name = source["name"];
	        this.noticeName = source["noticeName"];
	        this.company = source["company"];
	        this.stock = source["stock"];
	        this.price = source["price"];
	        this.value = source["value"];
	        this.effectiveDate = source["effectiveDate"];
	        this.graceUntil = source["graceUntil"];
	        this.noticeNo = source["noticeNo"];
	    }
	}
	export class Dashboard {
	    today: string;
	    upcoming: CutDate[];
	    deletions: DeletionRow[];
	    latestNotice?: NoticeView;
	    crawl: CrawlStatus;
	    lastPurchase: string;
	    lastDispense: string;
	    baseline: string;
	    products: number;
	    pending: Pending;
	    warnings: string[];
	    health: HealthItem[];
	
	    static createFrom(source: any = {}) {
	        return new Dashboard(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.today = source["today"];
	        this.upcoming = this.convertValues(source["upcoming"], CutDate);
	        this.deletions = this.convertValues(source["deletions"], DeletionRow);
	        this.latestNotice = this.convertValues(source["latestNotice"], NoticeView);
	        this.crawl = this.convertValues(source["crawl"], CrawlStatus);
	        this.lastPurchase = source["lastPurchase"];
	        this.lastDispense = source["lastDispense"];
	        this.baseline = source["baseline"];
	        this.products = source["products"];
	        this.pending = this.convertValues(source["pending"], Pending);
	        this.warnings = source["warnings"];
	        this.health = this.convertValues(source["health"], HealthItem);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DataDirInfo {
	    dir: string;
	    source: string;
	    sourceLabel: string;
	    canMove: boolean;
	    moveNote: string;
	    defaultDir: string;
	    locationFile: string;
	    logDir: string;
	    backupDir: string;
	    backupCustom: boolean;
	    backupNote: string;
	    dbSize: number;
	    backupCount: number;
	    lastBackup: string;
	
	    static createFrom(source: any = {}) {
	        return new DataDirInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dir = source["dir"];
	        this.source = source["source"];
	        this.sourceLabel = source["sourceLabel"];
	        this.canMove = source["canMove"];
	        this.moveNote = source["moveNote"];
	        this.defaultDir = source["defaultDir"];
	        this.locationFile = source["locationFile"];
	        this.logDir = source["logDir"];
	        this.backupDir = source["backupDir"];
	        this.backupCustom = source["backupCustom"];
	        this.backupNote = source["backupNote"];
	        this.dbSize = source["dbSize"];
	        this.backupCount = source["backupCount"];
	        this.lastBackup = source["lastBackup"];
	    }
	}
	
	export class DrugHit {
	    code: string;
	    name: string;
	    company: string;
	    spec: string;
	    baseLabel: string;
	    price: number;
	    inMaster: boolean;
	    pharmacy: boolean;
	    stock: number;
	    unit: string;
	    matchedBy: string;
	
	    static createFrom(source: any = {}) {
	        return new DrugHit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.name = source["name"];
	        this.company = source["company"];
	        this.spec = source["spec"];
	        this.baseLabel = source["baseLabel"];
	        this.price = source["price"];
	        this.inMaster = source["inMaster"];
	        this.pharmacy = source["pharmacy"];
	        this.stock = source["stock"];
	        this.unit = source["unit"];
	        this.matchedBy = source["matchedBy"];
	    }
	}
	export class PriceHistory {
	    effectiveDate: string;
	    detail: string;
	    before: number;
	    after: number;
	    noticeNo: string;
	    published: string;
	
	    static createFrom(source: any = {}) {
	        return new PriceHistory(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.effectiveDate = source["effectiveDate"];
	        this.detail = source["detail"];
	        this.before = source["before"];
	        this.after = source["after"];
	        this.noticeNo = source["noticeNo"];
	        this.published = source["published"];
	    }
	}
	export class DrugDetail {
	    code: string;
	    name: string;
	    company: string;
	    spec: string;
	    baseLabel: string;
	    price: number;
	    inMaster: boolean;
	    pharmacy: boolean;
	    stock: number;
	    unit: string;
	    matchedBy: string;
	    route: string;
	    ingredientCode: string;
	    otc: string;
	    payNote: string;
	    listDate: string;
	    barcodes: BarcodeInfo[];
	    prices: PriceHistory[];
	    monthlyUse: number;
	    lastPurchase: string;
	    lastPriceUnit: number;
	    lastSupplier: string;
	    lastDispense: string;
	    unitStatus: string;
	    unitNote: string;
	    sameIngredient: DrugHit[];
	
	    static createFrom(source: any = {}) {
	        return new DrugDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.name = source["name"];
	        this.company = source["company"];
	        this.spec = source["spec"];
	        this.baseLabel = source["baseLabel"];
	        this.price = source["price"];
	        this.inMaster = source["inMaster"];
	        this.pharmacy = source["pharmacy"];
	        this.stock = source["stock"];
	        this.unit = source["unit"];
	        this.matchedBy = source["matchedBy"];
	        this.route = source["route"];
	        this.ingredientCode = source["ingredientCode"];
	        this.otc = source["otc"];
	        this.payNote = source["payNote"];
	        this.listDate = source["listDate"];
	        this.barcodes = this.convertValues(source["barcodes"], BarcodeInfo);
	        this.prices = this.convertValues(source["prices"], PriceHistory);
	        this.monthlyUse = source["monthlyUse"];
	        this.lastPurchase = source["lastPurchase"];
	        this.lastPriceUnit = source["lastPriceUnit"];
	        this.lastSupplier = source["lastSupplier"];
	        this.lastDispense = source["lastDispense"];
	        this.unitStatus = source["unitStatus"];
	        this.unitNote = source["unitNote"];
	        this.sameIngredient = this.convertValues(source["sameIngredient"], DrugHit);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class FolderCheck {
	    path: string;
	    empty: boolean;
	    hasData: boolean;
	    newer: boolean;
	    same: boolean;
	    writable: boolean;
	    lastWrite: string;
	    note: string;
	
	    static createFrom(source: any = {}) {
	        return new FolderCheck(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.empty = source["empty"];
	        this.hasData = source["hasData"];
	        this.newer = source["newer"];
	        this.same = source["same"];
	        this.writable = source["writable"];
	        this.lastWrite = source["lastWrite"];
	        this.note = source["note"];
	    }
	}
	
	export class IDName {
	    id: number;
	    name: string;
	
	    static createFrom(source: any = {}) {
	        return new IDName(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	    }
	}
	export class ImportPreview {
	    kind: string;
	    fileName: string;
	    schema: ingest.Schema;
	    sheets: ingest.Preview[];
	    usable: string[];
	    needsMapping: boolean;
	    profileUsed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ImportPreview(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.fileName = source["fileName"];
	        this.schema = this.convertValues(source["schema"], ingest.Schema);
	        this.sheets = this.convertValues(source["sheets"], ingest.Preview);
	        this.usable = source["usable"];
	        this.needsMapping = source["needsMapping"];
	        this.profileUsed = source["profileUsed"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ImportRequest {
	    kind: string;
	    fileName: string;
	    dataBase64: string;
	    path: string;
	    overrides: ingest.Override[];
	    baselineDate: string;
	    timing: string;
	    asOf: string;
	    productCode: string;
	    saveProfile: boolean;
	    force: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ImportRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.fileName = source["fileName"];
	        this.dataBase64 = source["dataBase64"];
	        this.path = source["path"];
	        this.overrides = this.convertValues(source["overrides"], ingest.Override);
	        this.baselineDate = source["baselineDate"];
	        this.timing = source["timing"];
	        this.asOf = source["asOf"];
	        this.productCode = source["productCode"];
	        this.saveProfile = source["saveProfile"];
	        this.force = source["force"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ImportResult {
	    batchId: number;
	    kind: string;
	    fileName: string;
	    rows: number;
	    rangeFrom: string;
	    rangeTo: string;
	    replaced: number;
	    products: number;
	    suppliers: number;
	    unresolved: number;
	    issueCount: number;
	    issues: ingest.RowIssue[];
	    message: string;
	    needsMapping: boolean;
	    needsConfirm: boolean;
	    preview?: ImportPreview;
	    noticeId: number;
	    durationMs: number;
	
	    static createFrom(source: any = {}) {
	        return new ImportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.batchId = source["batchId"];
	        this.kind = source["kind"];
	        this.fileName = source["fileName"];
	        this.rows = source["rows"];
	        this.rangeFrom = source["rangeFrom"];
	        this.rangeTo = source["rangeTo"];
	        this.replaced = source["replaced"];
	        this.products = source["products"];
	        this.suppliers = source["suppliers"];
	        this.unresolved = source["unresolved"];
	        this.issueCount = source["issueCount"];
	        this.issues = this.convertValues(source["issues"], ingest.RowIssue);
	        this.message = source["message"];
	        this.needsMapping = source["needsMapping"];
	        this.needsConfirm = source["needsConfirm"];
	        this.preview = this.convertValues(source["preview"], ImportPreview);
	        this.noticeId = source["noticeId"];
	        this.durationMs = source["durationMs"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class InventoryRow {
	    code: string;
	    name: string;
	    unit: string;
	    qty: number;
	    estimate: number;
	    basis: string;
	    anchorDate: string;
	    anchorTiming: string;
	    anchorQty: number;
	    inflow: number;
	    outflow: number;
	    unitPrice: number;
	    value: number;
	    countQty?: number;
	    countDate: string;
	    unitStatus: string;
	    unitNote: string;
	    monthlyUse: number;
	    flags: string[];
	    hint: string;
	    acked: string[];
	
	    static createFrom(source: any = {}) {
	        return new InventoryRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.name = source["name"];
	        this.unit = source["unit"];
	        this.qty = source["qty"];
	        this.estimate = source["estimate"];
	        this.basis = source["basis"];
	        this.anchorDate = source["anchorDate"];
	        this.anchorTiming = source["anchorTiming"];
	        this.anchorQty = source["anchorQty"];
	        this.inflow = source["inflow"];
	        this.outflow = source["outflow"];
	        this.unitPrice = source["unitPrice"];
	        this.value = source["value"];
	        this.countQty = source["countQty"];
	        this.countDate = source["countDate"];
	        this.unitStatus = source["unitStatus"];
	        this.unitNote = source["unitNote"];
	        this.monthlyUse = source["monthlyUse"];
	        this.flags = source["flags"];
	        this.hint = source["hint"];
	        this.acked = source["acked"];
	    }
	}
	export class InventoryView {
	    asOf: string;
	    baseline: string;
	    baselineTiming: string;
	    lastPurchase: string;
	    lastDispense: string;
	    rows: InventoryRow[];
	    totalValue: number;
	    products: number;
	    negative: number;
	
	    static createFrom(source: any = {}) {
	        return new InventoryView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.asOf = source["asOf"];
	        this.baseline = source["baseline"];
	        this.baselineTiming = source["baselineTiming"];
	        this.lastPurchase = source["lastPurchase"];
	        this.lastDispense = source["lastDispense"];
	        this.rows = this.convertValues(source["rows"], InventoryRow);
	        this.totalValue = source["totalValue"];
	        this.products = source["products"];
	        this.negative = source["negative"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class JobRun {
	    id: number;
	    job: string;
	    startedAt: string;
	    finishedAt: string;
	    status: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new JobRun(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.job = source["job"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
	        this.status = source["status"];
	        this.message = source["message"];
	    }
	}
	export class KeyCheck {
	    ok: boolean;
	    pending: boolean;
	    total: number;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new KeyCheck(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.pending = source["pending"];
	        this.total = source["total"];
	        this.message = source["message"];
	    }
	}
	export class LotEvidenceView {
	    id: number;
	    source: string;
	    supplierId: number;
	    supplierName: string;
	    date: string;
	    qty: number;
	    lot: string;
	    expiry: string;
	    code: string;
	
	    static createFrom(source: any = {}) {
	        return new LotEvidenceView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.source = source["source"];
	        this.supplierId = source["supplierId"];
	        this.supplierName = source["supplierName"];
	        this.date = source["date"];
	        this.qty = source["qty"];
	        this.lot = source["lot"];
	        this.expiry = source["expiry"];
	        this.code = source["code"];
	    }
	}
	export class LotInput {
	    code: string;
	    supplierId: number;
	    date: string;
	    qty: number;
	    lot: string;
	    expiry: string;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new LotInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.supplierId = source["supplierId"];
	        this.date = source["date"];
	        this.qty = source["qty"];
	        this.lot = source["lot"];
	        this.expiry = source["expiry"];
	        this.source = source["source"];
	    }
	}
	export class LotPhotoRequest {
	    imageBase64: string;
	    code: string;
	
	    static createFrom(source: any = {}) {
	        return new LotPhotoRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.imageBase64 = source["imageBase64"];
	        this.code = source["code"];
	    }
	}
	export class LotPhotoRow {
	    code: string;
	    date: string;
	    supplierRaw: string;
	    supplierId: number;
	    supplierName: string;
	    qty: number;
	    expiry: string;
	    lot: string;
	    matched: string;
	    unsure: string[];
	
	    static createFrom(source: any = {}) {
	        return new LotPhotoRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.date = source["date"];
	        this.supplierRaw = source["supplierRaw"];
	        this.supplierId = source["supplierId"];
	        this.supplierName = source["supplierName"];
	        this.qty = source["qty"];
	        this.expiry = source["expiry"];
	        this.lot = source["lot"];
	        this.matched = source["matched"];
	        this.unsure = source["unsure"];
	    }
	}
	export class LotPhotoResult {
	    rows: LotPhotoRow[];
	    mode: string;
	    note: string;
	    rawText: string;
	    skipped: number;
	
	    static createFrom(source: any = {}) {
	        return new LotPhotoResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rows = this.convertValues(source["rows"], LotPhotoRow);
	        this.mode = source["mode"];
	        this.note = source["note"];
	        this.rawText = source["rawText"];
	        this.skipped = source["skipped"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class LotView {
	    id: number;
	    source: string;
	    supplierId: number;
	    supplierName: string;
	    date: string;
	    qty: number;
	    lot: string;
	    expiry: string;
	
	    static createFrom(source: any = {}) {
	        return new LotView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.source = source["source"];
	        this.supplierId = source["supplierId"];
	        this.supplierName = source["supplierName"];
	        this.date = source["date"];
	        this.qty = source["qty"];
	        this.lot = source["lot"];
	        this.expiry = source["expiry"];
	    }
	}
	export class MasterResult {
	    skipped: boolean;
	    updated: boolean;
	    count: number;
	    listDate: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new MasterResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.skipped = source["skipped"];
	        this.updated = source["updated"];
	        this.count = source["count"];
	        this.listDate = source["listDate"];
	        this.message = source["message"];
	    }
	}
	export class NoticePostResult {
	    noticeId: number;
	    already: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new NoticePostResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.noticeId = source["noticeId"];
	        this.already = source["already"];
	        this.message = source["message"];
	    }
	}
	export class NoticePostsResult {
	    imported: number;
	    already: number;
	    failed: string[];
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new NoticePostsResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.imported = source["imported"];
	        this.already = source["already"];
	        this.failed = source["failed"];
	        this.message = source["message"];
	    }
	}
	
	export class Notification {
	    id: number;
	    createdAt: string;
	    level: string;
	    title: string;
	    body: string;
	    link: string;
	    readAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Notification(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.createdAt = source["createdAt"];
	        this.level = source["level"];
	        this.title = source["title"];
	        this.body = source["body"];
	        this.link = source["link"];
	        this.readAt = source["readAt"];
	    }
	}
	
	export class PositionView {
	    supplierId: number;
	    supplierName: string;
	    rule?: domain.CompRule;
	    ruleText: string;
	    windowFrom: string;
	    windowTo: string;
	    purchased: number;
	    returned: number;
	    estimatedHeld: number;
	    scannedHeld?: number;
	    docQty: number;
	    heldUsed: number;
	    method: string;
	    recoverQty: number;
	    recovery: number;
	    isDefaultRule: boolean;
	    needsReview: boolean;
	    overridden: boolean;
	    settlementDate: string;
	    supplierKind: string;
	    scannedUnits: number;
	    registeredUnits: number;
	
	    static createFrom(source: any = {}) {
	        return new PositionView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.supplierId = source["supplierId"];
	        this.supplierName = source["supplierName"];
	        this.rule = this.convertValues(source["rule"], domain.CompRule);
	        this.ruleText = source["ruleText"];
	        this.windowFrom = source["windowFrom"];
	        this.windowTo = source["windowTo"];
	        this.purchased = source["purchased"];
	        this.returned = source["returned"];
	        this.estimatedHeld = source["estimatedHeld"];
	        this.scannedHeld = source["scannedHeld"];
	        this.docQty = source["docQty"];
	        this.heldUsed = source["heldUsed"];
	        this.method = source["method"];
	        this.recoverQty = source["recoverQty"];
	        this.recovery = source["recovery"];
	        this.isDefaultRule = source["isDefaultRule"];
	        this.needsReview = source["needsReview"];
	        this.overridden = source["overridden"];
	        this.settlementDate = source["settlementDate"];
	        this.supplierKind = source["supplierKind"];
	        this.scannedUnits = source["scannedUnits"];
	        this.registeredUnits = source["registeredUnits"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class ProductCodeInfo {
	    code: string;
	    name: string;
	    source: string;
	
	    static createFrom(source: any = {}) {
	        return new ProductCodeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.name = source["name"];
	        this.source = source["source"];
	    }
	}
	export class SettlementView {
	    id: number;
	    effectiveDate: string;
	    code: string;
	    name: string;
	    supplierId: number;
	    supplierName: string;
	    method: string;
	    qty: number;
	    unitDelta: number;
	    amount: number;
	    status: string;
	    memo: string;
	    ruleSnapshot: string;
	    doneAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new SettlementView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.effectiveDate = source["effectiveDate"];
	        this.code = source["code"];
	        this.name = source["name"];
	        this.supplierId = source["supplierId"];
	        this.supplierName = source["supplierName"];
	        this.method = source["method"];
	        this.qty = source["qty"];
	        this.unitDelta = source["unitDelta"];
	        this.amount = source["amount"];
	        this.status = source["status"];
	        this.memo = source["memo"];
	        this.ruleSnapshot = source["ruleSnapshot"];
	        this.doneAt = source["doneAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class ScanView {
	    id: number;
	    effectiveDate: string;
	    code: string;
	    gtin: string;
	    lot: string;
	    expiry: string;
	    serial: string;
	    raw: string;
	    human: string;
	    qty: number;
	    matchStatus: string;
	    candidates: IDName[];
	    supplierId: number;
	    supplierName: string;
	    resolvedBy: string;
	    registered: boolean;
	    createdAt: string;
	
	    static createFrom(source: any = {}) {
	        return new ScanView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.effectiveDate = source["effectiveDate"];
	        this.code = source["code"];
	        this.gtin = source["gtin"];
	        this.lot = source["lot"];
	        this.expiry = source["expiry"];
	        this.serial = source["serial"];
	        this.raw = source["raw"];
	        this.human = source["human"];
	        this.qty = source["qty"];
	        this.matchStatus = source["matchStatus"];
	        this.candidates = this.convertValues(source["candidates"], IDName);
	        this.supplierId = source["supplierId"];
	        this.supplierName = source["supplierName"];
	        this.resolvedBy = source["resolvedBy"];
	        this.registered = source["registered"];
	        this.createdAt = source["createdAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class PurchaseView {
	    id: number;
	    date: string;
	    supplierId: number;
	    supplierName: string;
	    kind: string;
	    qty: number;
	    rawQty: number;
	    rawUnit: string;
	    factor: number;
	    packSize: number;
	    unitPrice: number;
	    lot: string;
	    expiry: string;
	    inWindow: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PurchaseView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.date = source["date"];
	        this.supplierId = source["supplierId"];
	        this.supplierName = source["supplierName"];
	        this.kind = source["kind"];
	        this.qty = source["qty"];
	        this.rawQty = source["rawQty"];
	        this.rawUnit = source["rawUnit"];
	        this.factor = source["factor"];
	        this.packSize = source["packSize"];
	        this.unitPrice = source["unitPrice"];
	        this.lot = source["lot"];
	        this.expiry = source["expiry"];
	        this.inWindow = source["inWindow"];
	    }
	}
	export class ScanStat {
	    units: number;
	    qty: number;
	    ambiguous: number;
	    unmatched: number;
	    registered: number;
	
	    static createFrom(source: any = {}) {
	        return new ScanStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.units = source["units"];
	        this.qty = source["qty"];
	        this.ambiguous = source["ambiguous"];
	        this.unmatched = source["unmatched"];
	        this.registered = source["registered"];
	    }
	}
	export class RankRow {
	    changeId: number;
	    noticeId: number;
	    noticeNo: string;
	    code: string;
	    name: string;
	    noticeName: string;
	    company: string;
	    unit: string;
	    noticeUnit: string;
	    spec: string;
	    detail: string;
	    flexible: boolean;
	    note: string;
	    priceBefore: number;
	    priceAfter: number;
	    rate: number;
	    currentUnitPrice: number;
	    priceSource: string;
	    unitFactor: number;
	    unitStatus: string;
	    unitNote: string;
	    baseLabel: string;
	    delta: number;
	    estimate: number;
	    stock: number;
	    basis: string;
	    countQty?: number;
	    countDate: string;
	    countAnchorId: number;
	    laterCountQty?: number;
	    laterCountDate: string;
	    laterCountAnchorId: number;
	    grossLoss: number;
	    totalDocQty: number;
	    recommendation: string;
	    recovery: number;
	    netLoss: number;
	    physicalCount: number;
	    physicalUpside: number;
	    upsideAmount: number;
	    scan: ScanStat;
	    settleStatus: string;
	
	    static createFrom(source: any = {}) {
	        return new RankRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.changeId = source["changeId"];
	        this.noticeId = source["noticeId"];
	        this.noticeNo = source["noticeNo"];
	        this.code = source["code"];
	        this.name = source["name"];
	        this.noticeName = source["noticeName"];
	        this.company = source["company"];
	        this.unit = source["unit"];
	        this.noticeUnit = source["noticeUnit"];
	        this.spec = source["spec"];
	        this.detail = source["detail"];
	        this.flexible = source["flexible"];
	        this.note = source["note"];
	        this.priceBefore = source["priceBefore"];
	        this.priceAfter = source["priceAfter"];
	        this.rate = source["rate"];
	        this.currentUnitPrice = source["currentUnitPrice"];
	        this.priceSource = source["priceSource"];
	        this.unitFactor = source["unitFactor"];
	        this.unitStatus = source["unitStatus"];
	        this.unitNote = source["unitNote"];
	        this.baseLabel = source["baseLabel"];
	        this.delta = source["delta"];
	        this.estimate = source["estimate"];
	        this.stock = source["stock"];
	        this.basis = source["basis"];
	        this.countQty = source["countQty"];
	        this.countDate = source["countDate"];
	        this.countAnchorId = source["countAnchorId"];
	        this.laterCountQty = source["laterCountQty"];
	        this.laterCountDate = source["laterCountDate"];
	        this.laterCountAnchorId = source["laterCountAnchorId"];
	        this.grossLoss = source["grossLoss"];
	        this.totalDocQty = source["totalDocQty"];
	        this.recommendation = source["recommendation"];
	        this.recovery = source["recovery"];
	        this.netLoss = source["netLoss"];
	        this.physicalCount = source["physicalCount"];
	        this.physicalUpside = source["physicalUpside"];
	        this.upsideAmount = source["upsideAmount"];
	        this.scan = this.convertValues(source["scan"], ScanStat);
	        this.settleStatus = source["settleStatus"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ProductDetail {
	    effectiveDate: string;
	    asOf: string;
	    row: RankRow;
	    decision: domain.ProductDecision;
	    positions: PositionView[];
	    unattributed: number;
	    packSize: number;
	    purchases: PurchaseView[];
	    lots: LotView[];
	    scans: ScanView[];
	    settlements: SettlementView[];
	    anchors: AnchorView[];
	    history: ChangeRow[];
	
	    static createFrom(source: any = {}) {
	        return new ProductDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.effectiveDate = source["effectiveDate"];
	        this.asOf = source["asOf"];
	        this.row = this.convertValues(source["row"], RankRow);
	        this.decision = this.convertValues(source["decision"], domain.ProductDecision);
	        this.positions = this.convertValues(source["positions"], PositionView);
	        this.unattributed = source["unattributed"];
	        this.packSize = source["packSize"];
	        this.purchases = this.convertValues(source["purchases"], PurchaseView);
	        this.lots = this.convertValues(source["lots"], LotView);
	        this.scans = this.convertValues(source["scans"], ScanView);
	        this.settlements = this.convertValues(source["settlements"], SettlementView);
	        this.anchors = this.convertValues(source["anchors"], AnchorView);
	        this.history = this.convertValues(source["history"], ChangeRow);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class RankTotals {
	    items: number;
	    held: number;
	    grossLoss: number;
	    recovery: number;
	    netLoss: number;
	    document: number;
	    physical: number;
	    check: number;
	    done: number;
	
	    static createFrom(source: any = {}) {
	        return new RankTotals(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.items = source["items"];
	        this.held = source["held"];
	        this.grossLoss = source["grossLoss"];
	        this.recovery = source["recovery"];
	        this.netLoss = source["netLoss"];
	        this.document = source["document"];
	        this.physical = source["physical"];
	        this.check = source["check"];
	        this.done = source["done"];
	    }
	}
	export class Ranking {
	    effectiveDate: string;
	    asOf: string;
	    isPast: boolean;
	    notices: string[];
	    rows: RankRow[];
	    totals: RankTotals;
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new Ranking(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.effectiveDate = source["effectiveDate"];
	        this.asOf = source["asOf"];
	        this.isPast = source["isPast"];
	        this.notices = source["notices"];
	        this.rows = this.convertValues(source["rows"], RankRow);
	        this.totals = this.convertValues(source["totals"], RankTotals);
	        this.warnings = source["warnings"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ReportGroup {
	    key: string;
	    label: string;
	    count: number;
	    qty: number;
	    amount: number;
	    document: number;
	    physical: number;
	    done: number;
	    planned: number;
	
	    static createFrom(source: any = {}) {
	        return new ReportGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.count = source["count"];
	        this.qty = source["qty"];
	        this.amount = source["amount"];
	        this.document = source["document"];
	        this.physical = source["physical"];
	        this.done = source["done"];
	        this.planned = source["planned"];
	    }
	}
	export class SettlementFilter {
	    effectiveDate: string;
	    from: string;
	    to: string;
	    code: string;
	    supplierId: number;
	    status: string;
	
	    static createFrom(source: any = {}) {
	        return new SettlementFilter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.effectiveDate = source["effectiveDate"];
	        this.from = source["from"];
	        this.to = source["to"];
	        this.code = source["code"];
	        this.supplierId = source["supplierId"];
	        this.status = source["status"];
	    }
	}
	export class Report {
	    filter: SettlementFilter;
	    total: ReportGroup;
	    byDate: ReportGroup[];
	    bySupplier: ReportGroup[];
	    byProduct: ReportGroup[];
	    rows: SettlementView[];
	
	    static createFrom(source: any = {}) {
	        return new Report(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.filter = this.convertValues(source["filter"], SettlementFilter);
	        this.total = this.convertValues(source["total"], ReportGroup);
	        this.byDate = this.convertValues(source["byDate"], ReportGroup);
	        this.bySupplier = this.convertValues(source["bySupplier"], ReportGroup);
	        this.byProduct = this.convertValues(source["byProduct"], ReportGroup);
	        this.rows = this.convertValues(source["rows"], SettlementView);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class ReportResult {
	    path: string;
	    issueUrl: string;
	    openUrl: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ReportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.issueUrl = source["issueUrl"];
	        this.openUrl = source["openUrl"];
	        this.message = source["message"];
	    }
	}
	export class RuleInput {
	    id: number;
	    supplierId: number;
	    validFrom: string;
	    rule: domain.CompRule;
	    needsReview: boolean;
	    memo: string;
	
	    static createFrom(source: any = {}) {
	        return new RuleInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.supplierId = source["supplierId"];
	        this.validFrom = source["validFrom"];
	        this.rule = this.convertValues(source["rule"], domain.CompRule);
	        this.needsReview = source["needsReview"];
	        this.memo = source["memo"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RuleView {
	    id: number;
	    supplierId: number;
	    validFrom: string;
	    rule: domain.CompRule;
	    needsReview: boolean;
	    memo: string;
	    description: string;
	    isDefault: boolean;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new RuleView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.supplierId = source["supplierId"];
	        this.validFrom = source["validFrom"];
	        this.rule = this.convertValues(source["rule"], domain.CompRule);
	        this.needsReview = source["needsReview"];
	        this.memo = source["memo"];
	        this.description = source["description"];
	        this.isDefault = source["isDefault"];
	        this.updatedAt = source["updatedAt"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ScanInput {
	    effectiveDate: string;
	    productCode: string;
	    raw: string;
	    qty: number;
	
	    static createFrom(source: any = {}) {
	        return new ScanInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.effectiveDate = source["effectiveDate"];
	        this.productCode = source["productCode"];
	        this.raw = source["raw"];
	        this.qty = source["qty"];
	    }
	}
	export class ScanResult {
	    status: string;
	    message: string;
	    unit?: ScanView;
	    parsed?: domain.GS1;
	    candidates: Candidate[];
	
	    static createFrom(source: any = {}) {
	        return new ScanResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.message = source["message"];
	        this.unit = this.convertValues(source["unit"], ScanView);
	        this.parsed = this.convertValues(source["parsed"], domain.GS1);
	        this.candidates = this.convertValues(source["candidates"], Candidate);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class Settings {
	    dataDir: string;
	    dbPath: string;
	    version: string;
	    baselineDate: string;
	    baselineTiming: string;
	    crawlEnabled: boolean;
	    crawlTimes: string;
	    crawlInclude: string;
	    crawlExclude: string;
	    lastBackup: string;
	    osTasks: boolean;
	    zoom: string;
	    theme: string;
	    checkOnStart: boolean;
	    stdAuto: boolean;
	    stdDate: string;
	    stdCheckedAt: string;
	    stdCount: number;
	    updateCheck: boolean;
	    updateRepo: string;
	    hasUpdateToken: boolean;
	    updateCheckedAt: string;
	    logDebug: boolean;
	    engine: string;
	    masterAuto: boolean;
	    hasDataGoKey: boolean;
	    masterDate: string;
	    masterCount: number;
	    masterSource: string;
	    masterFetchedAt: string;
	    masterFailedAt: string;
	    masterFailMsg: string;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dataDir = source["dataDir"];
	        this.dbPath = source["dbPath"];
	        this.version = source["version"];
	        this.baselineDate = source["baselineDate"];
	        this.baselineTiming = source["baselineTiming"];
	        this.crawlEnabled = source["crawlEnabled"];
	        this.crawlTimes = source["crawlTimes"];
	        this.crawlInclude = source["crawlInclude"];
	        this.crawlExclude = source["crawlExclude"];
	        this.lastBackup = source["lastBackup"];
	        this.osTasks = source["osTasks"];
	        this.zoom = source["zoom"];
	        this.theme = source["theme"];
	        this.checkOnStart = source["checkOnStart"];
	        this.stdAuto = source["stdAuto"];
	        this.stdDate = source["stdDate"];
	        this.stdCheckedAt = source["stdCheckedAt"];
	        this.stdCount = source["stdCount"];
	        this.updateCheck = source["updateCheck"];
	        this.updateRepo = source["updateRepo"];
	        this.hasUpdateToken = source["hasUpdateToken"];
	        this.updateCheckedAt = source["updateCheckedAt"];
	        this.logDebug = source["logDebug"];
	        this.engine = source["engine"];
	        this.masterAuto = source["masterAuto"];
	        this.hasDataGoKey = source["hasDataGoKey"];
	        this.masterDate = source["masterDate"];
	        this.masterCount = source["masterCount"];
	        this.masterSource = source["masterSource"];
	        this.masterFetchedAt = source["masterFetchedAt"];
	        this.masterFailedAt = source["masterFailedAt"];
	        this.masterFailMsg = source["masterFailMsg"];
	    }
	}
	export class SettingsInput {
	    zoom: string;
	    theme: string;
	    crawlEnabled: boolean;
	    crawlTimes: string;
	    crawlInclude: string;
	    crawlExclude: string;
	    checkOnStart: boolean;
	    stdAuto: boolean;
	    updateCheck: boolean;
	    updateRepo: string;
	    updateToken: string;
	    logDebug: boolean;
	    masterAuto: boolean;
	    dataGoKey: string;
	
	    static createFrom(source: any = {}) {
	        return new SettingsInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.zoom = source["zoom"];
	        this.theme = source["theme"];
	        this.crawlEnabled = source["crawlEnabled"];
	        this.crawlTimes = source["crawlTimes"];
	        this.crawlInclude = source["crawlInclude"];
	        this.crawlExclude = source["crawlExclude"];
	        this.checkOnStart = source["checkOnStart"];
	        this.stdAuto = source["stdAuto"];
	        this.updateCheck = source["updateCheck"];
	        this.updateRepo = source["updateRepo"];
	        this.updateToken = source["updateToken"];
	        this.logDebug = source["logDebug"];
	        this.masterAuto = source["masterAuto"];
	        this.dataGoKey = source["dataGoKey"];
	    }
	}
	
	export class SettlementInput {
	    id: number;
	    method: string;
	    qty: number;
	    amount: number;
	    recalc: boolean;
	    status: string;
	    memo: string;
	
	    static createFrom(source: any = {}) {
	        return new SettlementInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.method = source["method"];
	        this.qty = source["qty"];
	        this.amount = source["amount"];
	        this.recalc = source["recalc"];
	        this.status = source["status"];
	        this.memo = source["memo"];
	    }
	}
	
	export class StdCodeResult {
	    skipped: boolean;
	    updated: boolean;
	    snapshot: crawler.StdSnapshot;
	    rows: number;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new StdCodeResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.skipped = source["skipped"];
	        this.updated = source["updated"];
	        this.snapshot = this.convertValues(source["snapshot"], crawler.StdSnapshot);
	        this.rows = source["rows"];
	        this.message = source["message"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SupplierInput {
	    id: number;
	    name: string;
	    kind: string;
	    active: boolean;
	    contact: string;
	    memo: string;
	
	    static createFrom(source: any = {}) {
	        return new SupplierInput(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.active = source["active"];
	        this.contact = source["contact"];
	        this.memo = source["memo"];
	    }
	}
	export class SupplierView {
	    id: number;
	    name: string;
	    kind: string;
	    active: boolean;
	    deleted: boolean;
	    contact: string;
	    memo: string;
	    aliases: string[];
	    purchaseLines: number;
	    lastPurchase: string;
	    rule?: RuleView;
	    ruleCount: number;
	
	    static createFrom(source: any = {}) {
	        return new SupplierView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.kind = source["kind"];
	        this.active = source["active"];
	        this.deleted = source["deleted"];
	        this.contact = source["contact"];
	        this.memo = source["memo"];
	        this.aliases = source["aliases"];
	        this.purchaseLines = source["purchaseLines"];
	        this.lastPurchase = source["lastPurchase"];
	        this.rule = this.convertValues(source["rule"], RuleView);
	        this.ruleCount = source["ruleCount"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UnitOption {
	    key: string;
	    label: string;
	    factor: number;
	    hint: string;
	
	    static createFrom(source: any = {}) {
	        return new UnitOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.factor = source["factor"];
	        this.hint = source["hint"];
	    }
	}
	export class UnresolvedRow {
	    id: number;
	    sheet: string;
	    row: number;
	    name: string;
	    qty: number;
	    unitPrice: number;
	    spec: string;
	    candidates: Candidate[];
	
	    static createFrom(source: any = {}) {
	        return new UnresolvedRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sheet = source["sheet"];
	        this.row = source["row"];
	        this.name = source["name"];
	        this.qty = source["qty"];
	        this.unitPrice = source["unitPrice"];
	        this.spec = source["spec"];
	        this.candidates = this.convertValues(source["candidates"], Candidate);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UpdateInfo {
	    current: string;
	    latest: string;
	    available: boolean;
	    notes: string;
	    url: string;
	    asset: string;
	    size: number;
	    skipped: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.current = source["current"];
	        this.latest = source["latest"];
	        this.available = source["available"];
	        this.notes = source["notes"];
	        this.url = source["url"];
	        this.asset = source["asset"];
	        this.size = source["size"];
	        this.skipped = source["skipped"];
	        this.message = source["message"];
	    }
	}

}

