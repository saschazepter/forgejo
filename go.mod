module forgejo.org

go 1.26.0

toolchain go1.26.8

require (
	code.forgejo.org/forgejo-contrib/go-libravatar v0.0.0-20260301104140-add494e31dab
	code.forgejo.org/forgejo/actions-proto v0.8.0
	code.forgejo.org/forgejo/go-rpmutils v1.0.0
	code.forgejo.org/forgejo/levelqueue v1.1.0
	code.forgejo.org/forgejo/reply v1.0.2
	code.forgejo.org/forgejo/runner/v13 v13.1.0
	code.forgejo.org/go-chi/binding v1.0.1
	code.forgejo.org/go-chi/cache v1.0.1
	code.forgejo.org/go-chi/captcha v1.0.3
	code.forgejo.org/go-chi/session v1.1.0
	code.forgejo.org/xorm/xorm v1.4.1
	code.gitea.io/sdk/gitea v0.21.0
	codeberg.org/gusted/mcaptcha v0.0.0-20220723083913-4f3072e1d570
	connectrpc.com/connect v1.20.0
	github.com/42wim/httpsig v1.2.3
	github.com/42wim/sshsig v0.0.0-20250502153856-5100632e8920
	github.com/Azure/go-ntlmssp v0.1.1
	github.com/ProtonMail/go-crypto v1.4.1
	github.com/PuerkitoBio/goquery v1.13.0
	github.com/SaveTheRbtz/zstd-seekable-format-go/pkg v0.9.0
	github.com/alecthomas/chroma/v2 v2.23.1
	github.com/blakesmith/ar v0.0.0-20190502131153-809d4375e1fb
	github.com/blevesearch/bleve/v2 v2.6.1
	github.com/buildkite/terminal-to-html/v3 v3.17.1
	github.com/caddyserver/certmagic v0.25.4
	github.com/chi-middleware/proxy v1.1.1
	github.com/djherbis/buffer v1.2.0
	github.com/djherbis/nio/v3 v3.0.1
	github.com/dsnet/compress v0.0.2-0.20230904184137-39efe44ab707
	github.com/dustin/go-humanize v1.0.1
	github.com/editorconfig/editorconfig-core-go/v2 v2.6.4
	github.com/emersion/go-imap v1.2.1
	github.com/felixge/fgprof v0.9.5
	github.com/fsnotify/fsnotify v1.10.1
	github.com/gdgvda/cron v0.7.0
	github.com/gliderlabs/ssh v0.3.8
	github.com/go-ap/activitypub v0.0.0-20260208110334-902f6cf8c2cc
	github.com/go-ap/jsonld v0.0.0-20251216162253-e38fa664ea77
	github.com/go-chi/chi/v5 v5.3.2
	github.com/go-chi/cors v1.2.2
	github.com/go-co-op/gocron v1.37.0
	github.com/go-enry/go-enry/v2 v2.9.6
	github.com/go-ldap/ldap/v3 v3.4.14
	github.com/go-openapi/spec v0.22.11
	github.com/go-sql-driver/mysql v1.10.0
	github.com/go-webauthn/webauthn v0.16.5
	github.com/gobwas/glob v0.2.3
	github.com/gogs/chardet v0.0.0-20211120154057-b7413eaefb8f
	github.com/gogs/go-gogs-client v0.0.0-20210131175652-1d7215cd8d85
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/golang/freetype v0.0.0-20170609003504-e2365dfdc4a0
	github.com/google/go-github/v81 v81.0.0
	github.com/google/pprof v0.0.0-20260709163759-e2ebcbee243b
	github.com/google/uuid v1.6.0
	github.com/gorilla/feeds v1.2.0
	github.com/gorilla/sessions v1.4.0
	github.com/hashicorp/go-version v1.8.0
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/huandu/xstrings v1.5.0
	github.com/inbucket/html2text v1.0.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/jhillyerd/enmime/v2 v2.2.0
	github.com/json-iterator/go v1.1.12
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51
	github.com/klauspost/compress v1.19.2
	github.com/markbates/goth v1.82.0
	github.com/mattn/go-isatty v0.0.24
	github.com/mattn/go-sqlite3 v1.14.50
	github.com/meilisearch/meilisearch-go v0.36.2
	github.com/mholt/archives v0.1.5
	github.com/microcosm-cc/bluemonday v1.0.27
	github.com/minio/minio-go/v7 v7.1.0
	github.com/msteinert/pam/v2 v2.1.0
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822
	github.com/niklasfasching/go-org v1.9.1
	github.com/olivere/elastic/v7 v7.0.32
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.1
	github.com/pquerna/otp v1.5.0
	github.com/prometheus/client_golang v1.21.1
	github.com/redis/go-redis/v9 v9.22.0
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	github.com/sergi/go-diff v1.4.0
	github.com/sourcegraph/zoekt v0.0.0-20260114143800-c747a3bccc2a
	github.com/stretchr/testify v1.12.1
	github.com/syndtr/goleveldb v1.0.0
	github.com/ulikunitz/xz v0.5.16
	github.com/urfave/cli/v3 v3.11.0
	github.com/valyala/fastjson v1.6.10
	github.com/yohcop/openid-go v1.0.1
	github.com/yuin/goldmark v1.8.5
	github.com/yuin/goldmark-highlighting/v2 v2.0.0-20230729083705-37449abec8cc
	gitlab.com/gitlab-org/api/client-go v1.46.0
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/crypto v0.56.0
	golang.org/x/image v0.45.0
	golang.org/x/net v0.58.0
	golang.org/x/oauth2 v0.36.0
	golang.org/x/sync v0.22.0
	golang.org/x/sys v0.47.0
	golang.org/x/text v0.41.0
	golang.org/x/tools v0.49.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/gomail.v2 v2.0.0-20160411212932-81ebce5c23df
	gopkg.in/ini.v1 v1.67.3
	mvdan.cc/xurls/v2 v2.6.0
	xorm.io/builder v0.3.13
)

require (
	cloud.google.com/go/compute/metadata v0.9.0 // indirect
	filippo.io/edwards25519 v1.2.0 // indirect
	git.sr.ht/~mariusor/go-xsd-duration v0.0.0-20220703122237-02e73435a078 // indirect
	github.com/RoaringBitmap/roaring v1.9.4 // indirect
	github.com/RoaringBitmap/roaring/v2 v2.14.5 // indirect
	github.com/STARRY-S/zip v0.2.3 // indirect
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/andybalholm/cascadia v1.3.4 // indirect
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bits-and-blooms/bitset v1.24.2 // indirect
	github.com/blevesearch/bleve_index_api v1.4.1 // indirect
	github.com/blevesearch/geo v0.2.6 // indirect
	github.com/blevesearch/go-faiss v1.1.5 // indirect
	github.com/blevesearch/go-porterstemmer v1.0.3 // indirect
	github.com/blevesearch/gtreap v0.1.1 // indirect
	github.com/blevesearch/mmap-go v1.2.0 // indirect
	github.com/blevesearch/scorch_segment_api/v2 v2.4.10 // indirect
	github.com/blevesearch/segment v0.9.1 // indirect
	github.com/blevesearch/snowballstem v0.9.0 // indirect
	github.com/blevesearch/upsidedown_store_api v1.0.2 // indirect
	github.com/blevesearch/vellum v1.2.0 // indirect
	github.com/blevesearch/zapx/v11 v11.4.3 // indirect
	github.com/blevesearch/zapx/v12 v12.4.3 // indirect
	github.com/blevesearch/zapx/v13 v13.4.3 // indirect
	github.com/blevesearch/zapx/v14 v14.4.3 // indirect
	github.com/blevesearch/zapx/v15 v15.4.3 // indirect
	github.com/blevesearch/zapx/v16 v16.3.4 // indirect
	github.com/blevesearch/zapx/v17 v17.2.3 // indirect
	github.com/bmatcuk/doublestar v1.3.4 // indirect
	github.com/bmatcuk/doublestar/v4 v4.10.0 // indirect
	github.com/bodgit/plumbing v1.3.0 // indirect
	github.com/bodgit/sevenzip v1.6.1 // indirect
	github.com/bodgit/windows v1.0.1 // indirect
	github.com/boombuler/barcode v1.0.1 // indirect
	github.com/bradfitz/gomemcache v0.0.0-20260422231931-4d751bb6e37c // indirect
	github.com/caddyserver/zerossl v0.1.5 // indirect
	github.com/cention-sany/utf7 v0.0.0-20170124080048-26cad61bd60a // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/cloudflare/circl v1.6.3 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/davidmz/go-pageant v1.0.2 // indirect
	github.com/dlclark/regexp2 v1.11.5 // indirect
	github.com/emersion/go-sasl v0.0.0-20231106173351-e73c9f7bad43 // indirect
	github.com/fatih/color v1.19.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.1 // indirect
	github.com/go-ap/errors v0.0.0-20260208110149-e1b309365966 // indirect
	github.com/go-asn1-ber/asn1-ber v1.5.8 // indirect
	github.com/go-enry/go-oniguruma v1.2.1 // indirect
	github.com/go-fed/httpsig v1.1.0 // indirect
	github.com/go-ini/ini v1.67.0 // indirect
	github.com/go-openapi/jsonpointer v1.0.0 // indirect
	github.com/go-openapi/jsonreference v1.0.1 // indirect
	github.com/go-openapi/swag/conv v0.29.1 // indirect
	github.com/go-openapi/swag/jsonutils v0.29.1 // indirect
	github.com/go-openapi/swag/loading v0.29.1 // indirect
	github.com/go-openapi/swag/pools v0.29.1 // indirect
	github.com/go-openapi/swag/stringutils v0.29.1 // indirect
	github.com/go-openapi/swag/typeutils v0.29.1 // indirect
	github.com/go-openapi/swag/yamlutils v0.29.1 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/go-webauthn/x v0.2.3 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/gorilla/securecookie v1.1.2 // indirect
	github.com/grafana/regexp v0.0.0-20240607082908-2cb410fa05da // indirect
	github.com/grpc-ecosystem/go-grpc-middleware v1.4.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.8 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/klauspost/crc32 v1.3.0 // indirect
	github.com/klauspost/pgzip v1.2.6 // indirect
	github.com/libdns/libdns v1.1.1 // indirect
	github.com/mailru/easyjson v0.9.0 // indirect
	github.com/markbates/going v1.0.3 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-runewidth v0.0.21 // indirect
	github.com/mattn/go-shellwords v1.0.12 // indirect
	github.com/mholt/acmez/v3 v3.1.6 // indirect
	github.com/miekg/dns v1.1.72 // indirect
	github.com/mikelolasagasti/xz v1.0.1 // indirect
	github.com/minio/crc64nvme v1.1.1 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/minio/minlz v1.0.1 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/mrjones/oauth v0.0.0-20190623134757-126b35219450 // indirect
	github.com/mschoch/smat v0.2.0 // indirect
	github.com/nwaples/rardecode/v2 v2.2.0 // indirect
	github.com/olekukonko/errors v1.1.0 // indirect
	github.com/olekukonko/ll v0.0.9 // indirect
	github.com/olekukonko/tablewriter v1.0.7 // indirect
	github.com/onsi/ginkgo v1.16.5 // indirect
	github.com/onsi/gomega v1.34.1 // indirect
	github.com/opentracing/opentracing-go v1.2.0 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.22 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.62.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/rhysd/actionlint v1.7.12 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/sirupsen/logrus v1.10.2 // indirect
	github.com/sorairolake/lzip-go v0.3.8 // indirect
	github.com/sourcegraph/go-ctags v0.0.0-20250729094530-349a251d78d8 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/ssor/bom v0.0.0-20170718123548-6386211fdfcf // indirect
	github.com/stretchr/objx v0.5.3 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/zeebo/blake3 v0.2.4 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	go.etcd.io/bbolt v1.5.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.1 // indirect
	go.uber.org/zap/exp v0.3.0 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.3 // indirect
	go4.org v0.0.0-20230225012048-214862532bf5 // indirect
	golang.org/x/mod v0.40.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc v1.83.2 // indirect
	gopkg.in/alexcesaro/quotedprintable.v3 v3.0.0-20150716171945-2caba252f4dc // indirect
)

replace github.com/hashicorp/go-version => github.com/6543/go-version v1.3.1

replace github.com/mholt/archiver/v3 => code.forgejo.org/forgejo/archiver/v3 v3.5.1

replace github.com/gliderlabs/ssh => code.forgejo.org/forgejo/ssh v0.0.0-20241211213324-5fc306ca0616

replace git.sr.ht/~mariusor/go-xsd-duration => code.forgejo.org/forgejo/go-xsd-duration v0.0.0-20220703122237-02e73435a078
