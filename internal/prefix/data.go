package prefix

// firstNames contains common international first names for email prefix generation.
// Organized by origin: English, Chinese pinyin, European, other international, modern.
var firstNames = []string{
	// English - male
	"aaron", "adam", "adrian", "alan", "alex", "andrew", "anthony", "austin",
	"benjamin", "blake", "bradley", "brandon", "brian", "bruce", "caleb", "cameron",
	"carl", "carter", "charles", "chase", "christian", "christopher", "colin", "connor",
	"daniel", "david", "dean", "derek", "dominic", "douglas", "dylan", "edward",
	"eli", "eric", "ethan", "evan", "frank", "gabriel", "gary", "george",
	"grant", "greg", "harry", "henry", "howard", "hunter", "ian", "isaac",
	"jack", "jackson", "jacob", "james", "jason", "jeffrey", "jeremy", "jesse",
	"joel", "john", "jonathan", "jordan", "joseph", "joshua", "julian", "justin",
	"keith", "kenneth", "kevin", "kyle", "larry", "leon", "liam", "logan",
	"lucas", "luke", "marcus", "mark", "martin", "mason", "matthew", "max",
	"michael", "nathan", "nicholas", "noah", "oliver", "oscar", "owen", "patrick",
	"paul", "peter", "raymond", "richard", "robert", "ryan", "samuel", "scott",
	"sean", "stephen", "steven", "thomas", "timothy", "tyler", "vincent", "william",
	// English - female
	"abigail", "alice", "amanda", "amber", "amy", "andrea", "angela", "anna",
	"ashley", "bella", "beth", "bianca", "brenda", "brittany", "brooke", "caitlin",
	"caroline", "cassandra", "catherine", "charlotte", "chloe", "claire", "crystal", "daisy",
	"danielle", "diana", "donna", "elena", "elizabeth", "ella", "emily", "emma",
	"erica", "eva", "faith", "fiona", "florence", "grace", "hailey", "hannah",
	"harper", "hazel", "heather", "holly", "isla", "jade", "jane", "janet",
	"jasmine", "jennifer", "jessica", "jocelyn", "julia", "karen", "kate", "katherine",
	"kelly", "kimberly", "laura", "lauren", "lily", "linda", "lisa", "lucy",
	"luna", "madison", "maria", "martha", "maya", "megan", "melissa", "michelle",
	"monica", "morgan", "natalie", "nicole", "olivia", "paige", "patricia", "rachel",
	"rebecca", "rose", "ruby", "samantha", "sarah", "sophia", "stella", "susan",
	"tara", "taylor", "vanessa", "victoria", "violet", "vivian", "wendy", "zoe",
	// Chinese pinyin
	"bai", "bin", "chang", "chao", "cheng", "cong", "dian", "dong",
	"fang", "fei", "feng", "gang", "guang", "hai", "han", "hang",
	"hao", "hong", "hua", "jian", "jie", "jin", "jun", "kai",
	"kang", "lei", "liang", "lin", "ling", "long", "lun", "mei",
	"ming", "nan", "nian", "peng", "ping", "qian", "qiang", "qing",
	"quan", "ran", "rui", "shan", "sheng", "shuang", "song", "tao",
	"tian", "ting", "wan", "wei", "wen", "xiang", "xiao", "xin",
	"xun", "yan", "yang", "yao", "yong", "yun", "zhen", "zheng",
	// European
	"anders", "andre", "axel", "bjorn", "carlo", "carmen", "dieter", "erik",
	"felix", "finn", "franz", "fritz", "greta", "hans", "heidi", "hugo",
	"ingrid", "jan", "jurgen", "karl", "klaus", "kurt", "lars", "lena",
	"leon", "luca", "luis", "maja", "marco", "marie", "mario", "mateo",
	"mila", "nils", "nora", "olaf", "ole", "otto", "paolo", "pierre",
	"rafael", "rene", "rolf", "rosa", "sven", "theo", "tomas", "yves",
	"alba", "alejandro", "antonio", "carlos", "diego", "jorge", "lucia", "miguel",
	"pablo", "pedro", "sergio", "sofia", "andreas", "astrid", "gustav", "hanna",
	"henrik", "katrin", "magnus", "marit", "sigrid", "torsten", "werner", "willem",
	// Other international
	"akira", "amit", "anita", "arjun", "deepa", "eunji", "fatima", "hana",
	"haruto", "hassan", "hyun", "joon", "karim", "kenji", "kira", "leila",
	"mika", "minji", "nadia", "neha", "omar", "petra", "pooja", "priya",
	"rahul", "ravi", "riku", "sakura", "sora", "sung", "vera", "vikram",
	"yeon", "yuki", "yuto", "zahra", "bogdan", "costin", "daria", "ioana",
	"lucian", "mircea", "simona", "stefan", "anja", "emre", "farhan", "hamza",
	"indira", "kofi", "lina", "mahdi", "nabil", "olena", "pavlo", "rashid",
	"sadia", "tariq", "uma", "vimal", "youssef", "zainab", "adel", "cyrus",
	// Modern / trending
	"aspen", "autumn", "briar", "cedar", "ember", "fern", "haven", "iris",
	"juniper", "lark", "meadow", "orion", "phoenix", "reed", "river", "rowan",
	"sage", "sky", "storm", "atlas", "blaze", "colt", "dash", "echo",
	"fox", "hawk", "indie", "jett", "kai", "kit", "lane", "lex",
	"neo", "onyx", "pax", "quinn", "raven", "slate", "wren", "winter",
}

// lastNames contains common international last names for email prefix generation.
var lastNames = []string{
	// English / American
	"adams", "allen", "anderson", "bailey", "baker", "barnes", "bell", "bennett",
	"brooks", "brown", "butler", "campbell", "carter", "clark", "cole", "coleman",
	"collins", "cook", "cooper", "cox", "cruz", "davis", "diaz", "edwards",
	"ellis", "evans", "fisher", "flores", "ford", "foster", "garcia", "gomez",
	"gonzalez", "gray", "green", "griffin", "hall", "hamilton", "harris", "harrison",
	"hayes", "henderson", "hernandez", "hill", "howard", "hughes", "hunt", "jackson",
	"james", "jenkins", "johnson", "jones", "jordan", "kelly", "kennedy", "king",
	"knight", "lee", "lewis", "long", "lopez", "martin", "martinez", "mason",
	"miller", "mitchell", "moore", "morgan", "morris", "murphy", "murray", "nelson",
	"nguyen", "oliver", "ortiz", "parker", "perez", "perry", "peterson", "phillips",
	"powell", "price", "ramirez", "reed", "reyes", "reynolds", "richardson", "rivera",
	"roberts", "robinson", "rodriguez", "rogers", "ross", "russell", "sanchez", "sanders",
	"scott", "simmons", "smith", "stewart", "sullivan", "taylor", "thomas", "thompson",
	"torres", "turner", "walker", "ward", "washington", "watson", "webb", "white",
	"williams", "wilson", "wood", "wright", "young",
	// Chinese
	"bai", "cai", "cao", "chen", "cheng", "cui", "dai", "deng",
	"ding", "dong", "duan", "fan", "fang", "feng", "gao", "gu",
	"guo", "han", "hao", "hong", "hou", "hu", "huang", "jia",
	"jiang", "jin", "kang", "kong", "lai", "lei", "liang", "lin",
	"liu", "long", "luo", "mao", "meng", "pan", "peng", "qian",
	"qin", "ren", "shao", "shen", "shi", "song", "sun", "tan",
	"tang", "tian", "wan", "wang", "wei", "wen", "xia", "xiao",
	"xie", "xue", "yan", "yang", "yao", "yuan", "zeng", "zhang",
	"zhao", "zheng", "zhong", "zhou", "zhu",
	// European
	"andersen", "andersson", "bakke", "bauer", "becker", "berg", "braun", "christensen",
	"dahl", "devries", "dubois", "dupont", "eriksson", "fernandez", "ferrari", "fischer",
	"gustafsson", "hansen", "hoffmann", "janssen", "jensen", "johansson", "karlsson", "klein",
	"koch", "kowalski", "larsen", "larsson", "laurent", "lefebvre", "leroy", "lund",
	"meyer", "moen", "moreau", "mueller", "neumann", "nielsen", "nilsson", "olsson",
	"pedersen", "persson", "petersen", "rasmussen", "richter", "romano", "rossi", "russo",
	"schmidt", "schneider", "schroeder", "schwarz", "simon", "sorensen", "strand", "svensson",
	"wagner", "weber", "wolf", "zimmermann", "bianchi", "colombo", "conti", "esposito",
	"greco", "ricci", "romero", "novak", "horvat", "szabo", "popescu", "ionescu",
	"silva", "santos", "oliveira", "souza", "pereira", "costa", "ferreira", "ribeiro",
	"almeida", "carvalho", "bakker", "dekker", "koster", "mulder", "visser",
	// Other international
	"gupta", "kumar", "mehta", "patel", "sharma", "singh", "verma", "ahmed",
	"hassan", "hussain", "khan", "malik", "mustafa", "tanaka", "yamamoto", "watanabe",
	"suzuki", "sato", "takahashi", "nakamura", "kobayashi", "yoshida", "choi", "jung",
	"cho", "yoon", "jang", "lim", "park", "ivanov", "kozlov", "petrov",
	"popov", "sokolov", "volkov", "kuznetsov",
	// Nature / place-based
	"archer", "ash", "banks", "beck", "birch", "bishop", "bridge", "brook",
	"castle", "church", "cliff", "cross", "dale", "drake", "field", "frost",
	"grove", "hart", "heath", "lake", "lane", "marsh", "meadow", "moss",
	"north", "oak", "pine", "ridge", "rivers", "rock", "rose", "shore",
	"snow", "south", "spring", "stone", "swift", "thorn", "vale", "waters",
	"wells", "west", "winter",
}

// nicknames contains common online handles and gaming-style names.
var nicknames = []string{
	"shadow", "phantom", "ghost", "spirit", "mystic", "cosmic", "stellar", "lunar",
	"solar", "phoenix", "dragon", "tiger", "eagle", "falcon", "wolf", "hawk",
	"viper", "cobra", "panther", "lion", "thunder", "storm", "blaze", "frost",
	"flame", "crystal", "spark", "bolt", "flash", "nova", "nebula", "aurora",
	"zenith", "apex", "prime", "omega", "alpha", "sigma", "delta", "echo",
	"ninja", "samurai", "warrior", "knight", "ranger", "scout", "pilot", "captain",
	"titan", "legend", "cyber", "pixel", "byte", "neo", "matrix", "vector",
	"quantum", "cipher", "proxy", "daemon", "golden", "silver", "crimson", "azure",
	"scarlet", "emerald", "violet", "onyx", "ivory", "obsidian", "silent", "swift",
	"brave", "wild", "fierce", "bold", "rogue", "rebel", "drifter", "nomad",
	"cloud", "ocean", "forest", "mountain", "desert", "island", "valley", "glacier",
	"comet", "orbit", "lucky", "happy", "sunny", "jolly", "clever", "witty",
	"chill", "mellow", "groovy", "funky", "ace", "pro", "zen", "rex",
	"dex", "jax", "zed", "dash", "flux", "rocket", "meteor", "astro",
	"galaxy", "cosmos", "pulsar", "quasar", "photon", "neutron", "magic", "wizard",
	"sage", "oracle", "druid", "mage", "rune", "alchemist", "sorcerer", "velvet",
	"chrome", "neon", "carbon", "cobalt", "mercury", "copper", "bronze", "platinum",
	"midnight", "twilight", "dawn", "ember", "cinder", "smoke", "haze", "mist",
	"vapor", "aether", "turbo", "nitro", "hyper", "ultra", "mega", "rapid",
	"sonic", "tempo", "rhythm", "pulse", "atlas", "maverick", "specter", "vertex",
	"prism", "nexus", "helix", "vortex", "maple", "cedar", "willow", "aspen",
	"laurel", "basil", "coral", "jade", "ruby", "opal",
}

// adjectives contains descriptive words used in nickname-style prefixes.
var adjectives = []string{
	"happy", "lucky", "bright", "swift", "brave", "calm", "clever", "cool",
	"crisp", "dark", "deep", "eager", "fair", "fast", "fierce", "fine",
	"firm", "free", "fresh", "glad", "good", "grand", "great", "keen",
	"kind", "light", "live", "loud", "mild", "neat", "nice", "noble",
	"odd", "pale", "plain", "proud", "pure", "quick", "rare", "raw",
	"real", "rich", "rough", "round", "safe", "sharp", "shy", "silent",
	"slim", "slow", "small", "smart", "smooth", "soft", "solid", "steady",
	"steep", "still", "strong", "super", "sweet", "tall", "thick", "thin",
	"tight", "tiny", "tough", "true", "vast", "warm", "wide", "wild",
	"wise", "young", "lazy", "crazy", "dizzy", "fuzzy", "witty", "fancy",
	"funky", "jolly", "misty", "dusty", "rusty", "stormy", "cloudy", "frosty",
	"snowy", "sunny", "rainy", "windy", "breezy", "cosmic", "magic", "royal",
	"golden", "silver", "velvet", "atomic", "digital", "neural", "sonic", "stellar",
	"polar", "arctic", "tropic", "electric", "iron", "copper", "marble", "frozen",
	"gentle", "humble", "vivid", "modern", "urban", "classic", "vintage", "retro",
}

// nouns contains objects and creatures used in nickname-style prefixes.
var nouns = []string{
	"star", "moon", "sun", "wolf", "bear", "fox", "hawk", "eagle",
	"lion", "tiger", "rose", "lily", "daisy", "iris", "fern", "oak",
	"pine", "elm", "ash", "ivy", "fire", "ice", "wind", "rain",
	"snow", "storm", "wave", "river", "lake", "cloud", "rock", "stone",
	"sand", "iron", "steel", "gold", "jade", "pearl", "ruby", "opal",
	"dawn", "dusk", "night", "dream", "song", "bell", "drum", "horn",
	"flute", "harp", "king", "knight", "prince", "duke", "ace", "sage",
	"monk", "chief", "scout", "guard", "spark", "flame", "bolt", "flash",
	"glow", "beam", "ray", "pulse", "arc", "blade", "shield", "arrow",
	"lance", "crown", "helm", "forge", "anvil", "gate", "tower", "cat",
	"owl", "dove", "swan", "crane", "raven", "robin", "wren", "lark",
	"finch", "ship", "sail", "mast", "port", "reef", "tide", "cove",
	"byte", "code", "chip", "node", "link", "grid", "mesh", "core",
	"loop", "stack", "glass", "silk", "wool", "lace", "cord", "wire",
	"ring", "chain", "gem", "prism",
}

// businessPrefixes contains common business email prefix words.
var businessPrefixes = []string{
	"info", "contact", "support", "admin", "help", "service", "sales", "office",
	"team", "hello", "mail", "post", "inbox", "notify", "alert", "news",
	"update", "reply", "welcome", "connect", "dev", "tech", "ops", "eng",
	"web", "cloud", "host", "sys", "api", "data", "biz", "corp",
	"pro", "vip", "exec", "lead", "studio", "lab", "hub", "works",
	"forge", "craft", "build", "design", "digital", "media", "press", "brand",
	"shop", "hiring",
}

// departments contains department or region tags for business email patterns.
var departments = []string{
	"tech", "sales", "legal", "finance", "ops", "dev", "design", "marketing",
	"support", "admin", "eng", "research", "security", "data", "platform", "product",
	"growth", "global", "local", "us", "eu", "asia", "uk", "hq",
	"central", "east", "west", "north", "south", "main", "team", "group",
	"dept", "unit", "core",
}

// nickPrefixWords are prepended to nicknames for variety.
var nickPrefixWords = []string{
	"the", "its", "im", "real", "just", "only", "hey", "not",
}

// nickSuffixTags are appended to nicknames for variety.
var nickSuffixTags = []string{
	"official", "real", "hq", "main", "pro", "vip",
}
