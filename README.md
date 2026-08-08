# diff-checker

Pembanding dokumen legal: menemukan perubahan teks, cacat penomoran, dan
referensi silang yang rusak antara dua versi dokumen.

Dirancang untuk dokumen hukum Indonesia — hierarki BAB / Bagian / Paragraf /
Pasal / ayat / huruf / angka sesuai kaidah UU 12/2011 — dan untuk kontrak
bergaya penomoran desimal (`Article 1`, `1.1`, `1.1.2`).

## Kenapa bukan sekadar "compare pakai AI"

Microsoft Word sudah bisa membandingkan teks. Yang tidak bisa dilakukan Word
adalah memberi tahu bahwa **menyisipkan satu Pasal membuat referensi silang di
bawahnya salah**. Itulah masalah yang dikerjakan proyek ini.

Prinsip desainnya: **deterministik dulu, LLM belakangan.**

| Pekerjaan | Cara |
|---|---|
| Parse heading, marker, hierarki | Go — regex + state machine |
| Cari lokasi perubahan teks | Go — Myers diff |
| Highlight kata yang berubah | Go — word-level diff |
| Validasi urutan penomoran | Go — walk marker sequence |
| Deteksi broken cross-reference | Go — grammar referensi + resolve ke tree |
| Klasifikasi makna & risiko legal | LLM — hanya di sini |

Konsekuensinya, setiap temuan diberi kelas yang tampil jelas:

- **`verified`** — hasil algoritma deterministik. Reproducible, tidak bisa
  halusinasi, nol biaya token.
- **`advisory`** — hasil LLM. Ditandai eksplisit sebagai saran.

## Status

Mesin deterministik, lapisan agentic, dan UI web sudah berjalan.
**Seluruh pemeriksaan penomoran dan referensi silang bekerja tanpa API key.**

| Komponen | Status |
|---|---|
| Ingest DOCX (termasuk tabel & tracked changes) | ✅ |
| Ingest PDF (via `pdftotext`) dan teks/Markdown | ✅ |
| Parser struktur + referensi silang | ✅ |
| Word-level diff + highlight | ✅ |
| Validator penomoran | ✅ |
| Validator integritas referensi | ✅ |
| CLI `diffctl` | ✅ |
| Lapisan agentic (trpc-agent-go) | ✅ |
| Grounding validator | ✅ |
| UI web + job queue + SSE | ✅ |
| Golden-set eval harness | ⬜ |
| Auto-fix DOCX | ⬜ |

## Prasyarat

Semuanya opsional kecuali Go — tanpa `poppler-utils`, ingest `.docx`/`.txt`/`.md`
tetap berjalan penuh; hanya input `.pdf` yang butuh alat ini.

| Alat | Untuk apa | Wajib? |
|---|---|---|
| Go 1.24+ | build & jalankan | ya |
| `poppler-utils` (`pdftotext`) | membaca input `.pdf` | hanya jika perlu ingest PDF |
| `libreoffice` (`soffice`) | `make fixtures` — generate ulang fixture uji | hanya untuk kontribusi ke `testdata/` |

Kenapa `pdftotext`, bukan library Go murni: dokumen hukum sering multi-kolom
dan bertabel, dan `pdftotext -layout` jauh lebih andal menjaga **urutan baca**
dibanding library ekstraksi PDF pure-Go yang tersedia gratis — parser struktur
Pasal/ayat/huruf bergantung pada urutan itu. Kalau tidak terpasang, `diffctl`
memberi pesan error dengan command instalasi yang tepat untuk OS kamu.

```bash
# macOS
brew install poppler

# Debian / Ubuntu
sudo apt install poppler-utils

# Fedora / RHEL
sudo dnf install poppler-utils

# Windows
# unduh dari https://github.com/oschwartz10612/poppler-windows
# lalu tambahkan folder bin/ ke PATH
```

Docker (`Dockerfile` di repo ini) sudah meng-install `poppler-utils` secara
otomatis — tidak perlu langkah tambahan untuk deploy via container.

## Menjalankan

```bash
make build            # -> bin/diffctl, bin/server
make test             # seluruh test
make check            # yang dijalankan CI: fmt, vet, test, verify
make demo             # bandingkan pasangan dokumen contoh
make demo-scenarios   # jalankan 6 skenario fixture DOCX
make run              # server web di :8080
```

### UI web

```bash
make run    # http://localhost:8080
```

Unggah dua dokumen, lihat checklist progres terisi lewat SSE, baca laporannya.
Tanpa API key, opsi analisis AI otomatis dinonaktifkan dan pemeriksaan
deterministik tetap berjalan penuh.

| Route | Isi |
|---|---|
| `GET /` | Form unggah + daftar perbandingan terakhir |
| `POST /compare` | Terima unggahan, buat job, redirect |
| `GET /jobs/{id}` | Halaman progres (checklist via SSE) |
| `GET /jobs/{id}/events` | Stream SSE |
| `GET /jobs/{id}/report` | Laporan lengkap |
| `GET /jobs/{id}/export.json` | Ekspor mentah — sekaligus kontrak API awal |

### Membandingkan dua dokumen

```bash
diffctl compare sebelum.docx sesudah.docx
diffctl compare sebelum.docx sesudah.docx --changes   # + daftar perubahan teks
diffctl compare sebelum.docx sesudah.docx --json      # laporan JSON
```

Status keluar bisa dipakai sebagai gerbang CI:
`0` bersih, `1` ada temuan mayor (dengan `--fail-on-major`), `2` ada temuan kritis.

### Dengan lapisan AI

```bash
export ANTHROPIC_API_KEY=sk-...
diffctl compare sebelum.docx sesudah.docx --ai
diffctl compare sebelum.docx sesudah.docx --ai --cost   # + rincian biaya token
```

Tanpa `--ai`, tidak ada satu pun panggilan jaringan.

### Memeriksa struktur satu dokumen

```bash
diffctl inspect dokumen.docx          # cetak pohon struktur
diffctl inspect dokumen.docx --refs   # + seluruh referensi silang
```

## Contoh keluaran

Pada pasangan contoh di `testdata/pair01/` — di mana Pasal 3 dihapus, huruf b
hilang, dan ayat (3) hilang:

```
Ringkasan perubahan
  ditambah   : 0
  dihapus    : 0
  diubah     : 2
  total      : 2
  jumlah pasal: 4 -> 3

Temuan (5 terverifikasi, 0 saran AI)
  kritis: 1  mayor: 4  minor: 0  info: 0

  [CRITICAL] Paragraf [13] merujuk ke Pasal 3, yang dihapus di versi baru —
             referensi menjadi menggantung  (broken_reference)
  [MAJOR]    huruf b hilang dalam Pasal 2 ayat (2) — melompat dari a ke c
             usul (renumber): [12] "c." -> "b."
  [MAJOR]    ayat 3 hilang dalam Pasal 2 — melompat dari 2 ke 4
  [MAJOR]    Pasal 3 hilang — melompat dari 2 ke 4
  [MAJOR]    Pasal 3 tidak lagi ada di versi baru
```

## Format yang didukung

| Format | Catatan |
|---|---|
| `.docx` | Paragraf termasuk isi tabel. Tracked changes diselesaikan ke tampilan *accepted*. |
| `.pdf` | Butuh `pdftotext`, lihat [Prasyarat](#prasyarat). PDF hasil scan ditolak dengan pesan jelas — OCR di luar cakupan v1. |
| `.txt`, `.md` | Satu baris = satu paragraf. |

## Arsitektur

```
internal/
  docmodel/    tipe inti: Paragraph, Node, Marker, Reference, Finding
  ingest/      DOCX / PDF / teks -> IndexedDoc ber-indeks [N]
  structure/   heading, marker, pohon hierarki, ekstraksi referensi
  textdiff/    word-level diff + highlight <b>
  rules/       validator penomoran & integritas referensi (deterministik)
  report/      perakitan laporan + renderer teks
  agentic/     graph LLM: state, node, prompt, tool, registry model
  ground/      validator grounding — aksi karangan model ditolak di sini
  jobs/        job store, worker pool, hub event SSE
  httpx/       handler HTTP + template + aset (embed)
cmd/
  diffctl/     CLI
  server/      web server
```

Skema pengalamatan `[N]` per paragraf konsisten di seluruh tahap: setiap temuan,
referensi, dan usulan aksi menunjuk paragraf dengan indeks yang sama.

### Pipeline

```
start ──┬── ingest_prev ──┐
        └── ingest_curr ──┴── deterministic ── triage ──┬── analyze ── recommend ──┐
                                                        └──────────────────────────┴── assemble
```

`deterministic` menjalankan seluruh mesin Fase 1 dan menghasilkan temuan
`verified`. Conditional edge sesudah `triage` melewati kedua node berbayar
ketika revisi ternyata hanya kosmetik — pengendali biaya utama desain ini.

Node `analyze` bersifat *agentic*: ia menerima ringkasan perubahan, bukan
seluruh dokumen, lalu memanggil tool Go (`get_paragraph`, `resolve_reference`,
`find_references_to`, …) untuk menarik konteks yang ia butuhkan sendiri.

### Konfigurasi model

Tiering per node, semuanya bisa di-override lewat environment:

| Tier | Default | Peran |
|---|---|---|
| `triage` | Claude Haiku 4.5 | kosmetik vs substantif |
| `analyze` | Claude Sonnet 5 | klasifikasi makna |
| `recommend` | Claude Opus 5 | risiko hukum + rekomendasi |

```bash
# Semua tier ke satu endpoint OpenAI-compatible
export DIFF_PROVIDER=openai DIFF_MODEL=deepseek-chat
export DIFF_BASE_URL=https://api.deepseek.com/v1 OPENAI_API_KEY=...

# Atau per tier
export DIFF_MODEL_RECOMMEND=claude-opus-5
export DIFF_RATE_IN_RECOMMEND=5 DIFF_RATE_OUT_RECOMMEND=25
```

## Menjamin output tepat

Lapisan yang membuat saran AI bisa dipercaya, dari yang paling murah:

1. **Structured output** — skema JSON diturunkan lewat refleksi dari struct Go.
2. **Grounding validator** (`internal/ground`) — setiap aksi dicek terhadap
   dokumen nyata. Teks `old` yang tidak muncul persis di paragraf yang disebut
   **ditolak**, modelnya diberi tahu apa yang salah, lalu diminta ulang sekali.
3. **Pemisahan verified/advisory** — temuan deterministik tidak pernah melewati
   LLM, jadi tidak bisa terdegradasi olehnya.
4. **Taksonomi & rubrik severity tertutup** — enum, bukan teks bebas, sehingga
   hasilnya bisa dievaluasi dan dibandingkan antar-run.

Kolom `cache read` pada rincian biaya harus &gt; 0 pada perbandingan kedua dan
seterusnya. Kalau selalu nol, ada yang membuat prefix prompt berubah antar
permintaan dan asumsi biaya tidak lagi berlaku.

## Fixture uji

`testdata/scenarios/` berisi enam pasang dokumen DOCX + PDF (masing-masing ≥3
halaman) yang mencakup perubahan teks murni, cacat penomoran, referensi rusak,
referensi yang pindah, kontrak berpenomoran desimal, dan gabungan ketiganya.
Temuan aktualnya didokumentasikan di
[`testdata/scenarios/MANIFEST.md`](testdata/scenarios/MANIFEST.md).

```bash
make fixtures         # regenerasi (butuh libreoffice-writer + poppler-utils)
make demo-scenarios   # jalankan semuanya lewat mesin deterministik
```

## Lisensi

MIT — lihat [LICENSE](LICENSE).
