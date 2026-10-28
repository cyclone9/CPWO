# CPWO - CyClone Power Web Optimizer

[English](README.md) | [فارسی](README.fa.md) | [Türkçe](README.tr.md) | [العربية](README.ar.md)

<p align="center"><picture><source media="(prefers-color-scheme: dark)" srcset="assets/cpwo-lockup-on-dark.svg"><img src="assets/cpwo-lockup-on-light.svg" alt="CPWO" width="348"></picture></p>

Sürüm 1.0.0 (2026-10-24). [CHANGELOG.md](CHANGELOG.md) dosyasına bakın.

Web sitesi: [cyclone9.dev/cpwo](https://cyclone9.dev/cpwo)

HTML, CSS ve JS dosyalarını küçülten, görselleri sıkıştırıp WebP ile AVIF'e dönüştüren tek bir binary.

cgo yok, çalıştığı makineye kurulacak bir şey de yok.

<p align="center"><a href="assets/cpwo-video-en.mp4"><img src="assets/cpwo-video-en.jpg" alt="CPWO videosu" width="720"></a></p>

<p align="center">CPWO'ya 24 saniyelik bir bakış. Videoyu açmak için görsele tıklayın. Video İngilizcedir.</p>

```
cpwo [flags] <project-path>...
```

CPWO'ya bir proje dizini verin. Dizini alt klasörleriyle birlikte tarar ve her
sonucu geldiği dosyanın yanına yazar. Kaynak dosyalarınız asla değiştirilmez.
Seçenekler yolların önüne de gelebilir, arkasına da; yani `cpwo ./site -avif` de
çalışır. Tek başına bir `--` işaretinden sonra gelen her şey yol sayılır.

```
style.css   ->  style.min.css        photo.jpg  ->  photo.webp
app.js      ->  app.min.js           photo.jpg  ->  photo-512.webp   (with -r)
index.html  ->  index.min.html       logo.png   ->  logo.avif        (with -avif)
```

Her çalıştırma, kök dizine ürettiği dosyaları listeleyen bir `.cpwo-output`
dosyası da yazar. Sonraki çalıştırma, CPWO'nun dosyalarını sizinkilerden ayırt
etmek için bu dosyayı okur.

## Seçenekler

| Seçenek | Varsayılan | Ne yapar |
|---|---|---|
| `-q <1-100>` | `82` | JPEG ve kayıplı WebP için kalite. AVIF buna göre ölçeklenir |
| `-r` | kapalı | Yeniden boyutlandırılmış varyantları da yazar, açık olan her biçimde |
| `-sizes <list>` | `1024,512,256` | `-r` için genişlikler. Verilirse `-r` de açılır |
| `-resize-fallback` | kapalı | Yeniden boyutlandırılmış JPEG/PNG'yi de varyantların yanına yazar. `-r` de açılır |
| `-relink` | kapalı | `<link href>` ve `<script src>` adreslerini var olan `.min.` dosyalarına yönlendirir. Güncel olma şartı yalnızca CPWO'nun kendi dosyaları için aranır |
| `-picture` | kapalı | `<img>` etiketini AVIF ve WebP `<source>` öğeleriyle `<picture>` içine alır. `srcset` ya da görsel yolu içeren bir `data-` özniteliği olan `<img>` etiketine dokunmaz |
| `-exif` | kapalı | Kaynaktaki EXIF'i yeniden kodlanan `.min.jpg` içinde korur |
| `-only` | kapalı | Yalnızca komut satırında adı geçen grupları çalıştırır |
| `-overwrite` | kapalı | CPWO'nun kendi çıktılarını güncel olsalar bile yeniden üretir. CPWO'nun üretmediği bir dosyanın üzerine asla yazmaz |
| `-adopt` | kapalı | CPWO'nun yazacağı yerde zaten duran dosyaları, sanki `.cpwo-output` listelemiş gibi devralır. Kaydın kaybolduğu ya da hiç commit edilmediği durumlar için. Oradaki üçüncü taraf dosyaları da devralınır, o yüzden önce `-v` ile bakın |
| `-j <n>` | CPU sayısı | İş sayısı, yani kaç dosyanın aynı anda işleneceği |
| `-v` | kapalı | Her dosya için bir satır da yazdırır, bir dosyaya neden dokunulmadığını da söyler. Özet yine yazdırılır |
| `-version` | | Sürümü yazdırır |
| `-license` | | CPWO'nun lisansını (MIT) telif hakkı satırıyla, ardından içine gömülü her bileşen için bir satır (ad, lisans, kullanım amacı) yazdırır. Tam metinler [licenses.md](licenses.md) dosyasındadır |
| `-help`, `-h` | | Kullanım bilgisini yazdırır |

### Çıkış kodu

| Kod | Anlamı |
|---|---|
| `0` | Tamamlandı |
| `1` | Bazı dosyalar işlenemedi. Her biri bildirilir, çalıştırmanın geri kalanı yine yapılır, kayıt dosyası dâhil |
| `2` | Yanlış kullanım ya da var olmayan bir yol. Her yol, iş başlamadan önce kontrol edilir |

### Neyin çalışacağını seçme

`-html -css -js -json -svg -xml -img -webp` varsayılan olarak hep açıktır.
Bunlardan birinin ya da birkaçının adını yazarsanız yalnızca onlar çalışır.

`-avif` bunun istisnasıdır: siz isteyene kadar kapalı olduğu için adını yazmak,
çalıştırmayı AVIF'e daraltmaz, normal çalıştırmaya AVIF'i *ekler*. Başka türlü
olamazdı: AVIF'i açmanın tek yolu adını yazmaktır; `-webp` gibi o da daraltsaydı
“her zamanki çalıştırma, bir de AVIF” demek için dokuz grubun hepsini tek tek
yazmak gerekirdi.

Onu tek başına çalıştırmanın yolu `-only` seçeneğidir ve her grup için işe yarar.

```sh
cpwo -webp ./site           # only WebP conversion
cpwo -avif ./site           # the usual run, with AVIF as well
cpwo -avif -only ./site     # AVIF and nothing else
cpwo -webp -avif ./site     # only the two image formats
cpwo -avif -js=false ./site # the usual run, plus AVIF, minus JS
cpwo -css -js ./site        # only CSS and JS
cpwo -js=false ./site       # everything except JS
```

Grup adı verilmeden kullanılan `-only`, hiçbir şey yapmadan biten bir çalıştırma
değil, hatadır.

`-img`, JPEG ve PNG'yi `-q` değeriyle yeniden sıkıştırır. `-webp` seçeneği `.webp`
ikizlerini, `-avif` seçeneği ise `.avif` olanları yazar.

### AVIF

AVIF, aynı görselin WebP hâlinden genellikle %30-50 daha küçüktür ve güncel her
tarayıcı onu destekler. Varsayılan olarak kapalı olmasının tek bir nedeni var:
AVIF kodlayıcısı WebP kodlayıcısından yaklaşık on kat yavaştır, bu da her
kaydettiğinizde çalıştırabileceğiniz bir araç için yanlış bir takastır. Release
derlemesinde açın, üzerinde çalışırken kapalı bırakın.

`-q`, JPEG/WebP ölçeğinde bir sayıdır ve AVIF aynı ölçeği kullanmaz: `-q 82` değeri
AVIF'e olduğu gibi verilirse WebP'den *daha büyük* bir dosya çıkar. CPWO bu
ölçeklemeyi sizin yerinize yapar; böylece tek bir `-q` üç biçimin hepsini kapsar ve
her birinde kabaca aynı anlama gelir.

Kamera çözünürlüğündeki fotoğraflarda her AVIF kodlaması bir gigabaytın çok
üzerinde bellek gerektirir. Varsayılan `-j` ile işlenen dört adet 12 megapiksellik
fotoğrafta bellek kullanımı en çok yaklaşık 5 GB'a ulaştı; o yüzden belleği az
olan bir makinede `-j` değerini düşürün.

### Yeniden boyutlandırma

```sh
cpwo -r ./site                    # 1024, 512 and 256 wide
cpwo -sizes 512,256,128 ./site    # your own widths, -r implied
```

En boy oranı korunur ve hiçbir görsel büyütülmez: 400px genişliğindeki
bir kaynak 512 ve 1024'ü atlar. Orijinal dosyaya dokunulmaz.

Açık olan her biçim yeniden boyutlandırılır: tek başına `-r` WebP varyantlarını,
`-r -avif` ise ikisini de verir:

```
cpwo -r ./site           photo.jpg  ->  photo-512.webp  photo-256.webp
cpwo -r -avif ./site     photo.jpg  ->  photo-512.webp  photo-256.webp
                                        photo-512.avif  photo-256.avif
```

Bir `<img srcset>` için aynı biçimde ikiz dosyalar istiyorsanız
`-resize-fallback` ekleyin:

```
photo.jpg  ->  photo-512.webp  photo-512.min.jpg
               photo-256.webp  photo-256.min.jpg
```

Fallback dosyasının adındaki `.min.` önemlidir. Varyantı sonraki taramanın dışında
tutar; böylece ikinci bir çalıştırma `photo-512.jpg` dosyasını
`photo-512-256.jpg` hâline getiremez.

## EXIF

Dikey çekilmiş bir fotoğraf genellikle yatay olarak saklanır ve görüntüleyiciye onu
döndürmesini söyleyen bir EXIF yön etiketi taşır. Go'nun görüntü kod çözücüsü bu
etiketi yok sayar ve kodlayıcıların hiçbiri onu geri yazmaz; o yüzden CPWO başka
bir şey yapmadan önce pikselleri kendisi döndürür. Her çıktı (`.min.jpg`, `.webp`,
`.avif` ve yeniden boyutlandırılmış her kopya), orijinalin tarayıcınızda göründüğü
yönle aynı yönde çıkar.

EXIF bloğunun geri kalanı varsayılan olarak atılır. Bir web görselinde gereksiz
yüktür: çoğu zaman onlarca kilobayt kamera ayarından ve gömülü bir küçük resimden
oluşur, GPS koordinatları da orada saklanır.

`-exif` bunun yerine bloğu korur. Tek bir durumda buna gerçekten ihtiyaç olur:
kaybetmek istemediğiniz yazar ve telif hakkı bilgisi.

```sh
cpwo -exif ./site
```

İki sınır var; ikisi de tercih değil, yapıdan kaynaklanıyor:

- Yalnızca yeniden kodlanan `.min.jpg` için geçerlidir. WebP ve AVIF bu bloğu
  taşıyamaz, çünkü buradaki kodlayıcılarda onu yazmanın bir yolu yok. Yeniden
  boyutlandırılmış bir kopya ise metadata'nın tarif ettiği görselden farklı bir
  görseldir; içinde kayıtlı boyutlar bile tutmaz.
- Yön etiketi geçişte 1'e sıfırlanır. CPWO pikselleri zaten döndürdüğü için hâlâ
  “beni döndür” diyen bir etiket, görüntüleyicinin aynı işi iki kez yapmasına yol
  açardı.

EXIF segmenti, `.min.jpg` dosyasını kaynaktan büyük yapacak kadar büyükse olağan
kural geçerli olur ve CPWO bunun yerine orijinali korur.

## HTML'i yeniden yazma

Bu iki yeniden yazma işlemi de varsayılan olarak kapalıdır, çünkü HTML'inizi
değiştirirler. İkisi de kaynak dosyaya dokunmaz. Yalnızca küçültülmüş kopya yeniden
yazılır.

`-relink`, bağlantıları küçültülmüş ikizlere yöneltir:

```html
<link rel="stylesheet" href="assets/css/style.css">     <!-- in  -->
<link rel=stylesheet href=assets/css/style.min.css>     <!-- out -->
```

`-picture`, görselleri sarmalar ve `<img>` etiketindeki her özniteliği korur:

```html
<img class="brand__tile" src="logo-128.png" alt="" width="46" height="46">
```
```html
<picture><source srcset="logo-128.avif" type="image/avif">
<source srcset="logo-128.webp" type="image/webp">
<img class="brand__tile" src="logo-128.png" alt="" width="46" height="46"></picture>
```

Sıra önemlidir: tarayıcı anladığı ilk `<source>` öğesini alır, bu nedenle en küçük
biçim en başta olmalıdır.

İkisi de ancak yerine konacak dosya diskte gerçekten varsa devreye girer; dosyayı
CPWO ürettiyse ayrıca, üretildiği dosyadan eski olmaması gerekir. Böylece CPWO, bir
sayfayı yazmamaya karar verdiği bir dosyaya ya da eskimiş bir kopyaya
yönlendiremez. Bir kütüphanenin kendi `.min.` dosyasının yalnızca var olması yeter:
arşivler herhangi bir sırayla açıldığı için dosyanın tarihi eşleşip eşleşmediği
hakkında bir şey söylemez. CPWO şunlara dokunmaz: dış URL'ler, `data:` ve site
kökünden başlayan `/…` bağlantıları, adında zaten `.min.` geçen her şey, zaten bir
`<picture>` içinde olan `<img>` etiketleri ve CSS olmayan `<link>` etiketleri
(örneğin `rel="icon"`).

`-picture`, `srcset` taşıyan bir `<img>` etiketine de dokunmaz: tam boyutlu tek bir
`<source>` her ekranda onun önüne geçerdi ve telefon büyük görseli indirirdi.
Aynısı, görsel yolu içeren bir `data-` özniteliği olan `<img>` için de geçerlidir,
çünkü lazy loading betiği o değeri sonradan `src` içine yerleştirir ve öne konan bir
`<source>` onu kalıcı olarak gizlerdi.

`-picture` bir biçimi yalnızca tarayıcının aksi hâlde alacağı her şeyi geçiyorsa
önerir. CPWO'nun uyduğu kural budur ([Koruma önlemleri](#koruma-önlemleri)
bölümüne bakın); bu nedenle bir `.avif`, görselden olduğu kadar `.webp` dosyasından
da küçük olmalıdır.

Görseller HTML'den önce işlenir, bu sayede kontrol tamamlanmış dosyaları görür. Bu
seçeneklerden biri açıkken HTML her çalıştırmada yeniden işlenir, çünkü sonuç
sayfanın kendi zaman damgasına değil, yanında hangi dosyaların bulunduğuna bağlıdır.

### Yeniden çalıştırma

Bir çıktı, var olduğunda ve kaynağından eski olmadığında atlanır; ikinci
çalıştırma bu yüzden hiçbir şey yapmaz ve hiçbir şeye mal olmaz. Tek bir dosyayı
düzenlerseniz yalnızca o dosya yeniden işlenir. `-overwrite` hepsini baştan yapar,
ama yalnızca CPWO'nun kendi çıktılarını. CPWO'nun üretmediği bir dosyanın üzerine
asla yazmaz.

Önceki çalıştırmalardan kalan çıktılar da yenileriyle aynı kurala tabidir
([Koruma önlemleri](#koruma-önlemleri) bölümüne bakın). Bir çıktı artık yerini hak
etmiyorsa CPWO onu siler: daha küçük bir `.webp` dosyasının artık geride bıraktığı
bir `.avif`, görsel değiştirildiği için kaynağını artık geçemeyen bir `.webp` ya da
`.min.png`, kaynak daha genişken üretilmiş yeniden boyutlandırılmış bir varyant.
Silinen dosyalar yalnızca `.cpwo-output` içinde listelenenlerdir. `-v` her birinin
adını yazar, özet satırı da sayısını verir (`N outdated removed`).

`-q` değerini değiştirip `-overwrite` ile çalıştırırsanız kaynağını artık geçemeyen
bir `.min.jpg` silinebilir; o yüzden `-q` değerini değiştirecekseniz `.min.jpg`
dosyalarına elle bağlantı vermeyin.

CPWO 0.2.1-beta ve öncesinin yazdığı kayıtlar, çıktı yolunda güncel buldukları her
dosyayı listeliyordu; elle yapılmış bir `.webp` de buna dâhildi. 0.3.0'dan itibaren
bu kayıtlardaki girdiler, üzerine yazılırken eskisi gibi CPWO'nun sayılır ama hiç
silinmez: eskimiş diye silinecek olan bir girdi, silinmek yerine kayıttan düşürülür
ve dokunulmadan bırakılır. CPWO böyle bir dosyayı kendisi yazana kadar
`.cpwo-output` onu bir başlık satırında, `# Kept as found, never deleted: <path>`
biçiminde belirtir; böylece bu kural sonraki her çalıştırmada da geçerli kalır.
`-adopt` ile devralınan dosyalar da aynı satırı alır.

Adında `.min.` geçen her şey ve `.webp` ya da `.avif` ile biten her şey atlanır.
Bu, CPWO'nun kendi çıktısı için de geçerlidir, bu sayede `style.min.min.css` hiç
oluşmaz; projeye hazır hâlde konmuş üçüncü taraf dosyaları için de: `bootstrap.min.css`
ve `bootstrap.bundle.min.js` geldikleri gibi bırakılır. `node_modules`, `vendor` ve
nokta ile başlayan dizinler hiç taranmaz.

## .cpwo-output dosyası

Proje başına bir tane. CPWO'nun projenize koyduklarını kaydeder, başka bir şeyi
değil.

```
# Written by CPWO v1.0.0 on 2026-08-21 16:55:57
# Result: 16 sources -> 21 generated files, 322.1 KB -> 259.0 KB (19.6% smaller)

# File Map

┌────────────────────────────┬────────────────────────────────┐
│ Source                     │ Generated                      │
├────────────────────────────┼────────────────────────────────┤
│ assets/css/style.css       │ assets/css/style.min.css       │
│ assets/img/logo-128.png    │ assets/img/logo-128.min.png    │
│                            │ assets/img/logo-128.webp       │
│ assets/js/app.js           │ assets/js/app.min.js           │
│ index.html                 │ index.min.html                 │
└────────────────────────────┴────────────────────────────────┘

# File Tree

.
├── assets
│   ├── css
│   │   └── style.min.css
│   ├── img
│   │   ├── logo-128.min.png
│   │   └── logo-128.webp
│   └── js
│       └── app.min.js
└── index.min.html
```

Yalnızca son çalıştırmanın yazdığı dosyaları değil, var olan tüm dosyaları yansıtır;
bu yüzden hiçbir şeyi değiştirmeyen bir yeniden çalıştırma bile tam listeyi verir.
Yollar göreli yazılır ve eğik çizgi kullanır, dosya da BOM içermeyen düz UTF-8'dir;
her yerde aynı okunur.

Her çalıştırma yaptıklarını mevcut dosyayla birleştirdiği için daha dar bir
çalıştırma (`cpwo -webp`) ya da tek bir dosya üzerindeki çalıştırma geri kalan her
şeyin kaydını korur. Bir dosya (kaydetme hook'unun ilettiği şey) ya da bir klasör
üzerindeki çalıştırma, bulunduğu yerdeki veya üstündeki en yakın `.cpwo-output`
dosyasına kaydedilir; arama bir git deposunun kökünde durur. Ancak hiçbiri yoksa
orada yeni bir tane başlatılır (dosya için dosyanın klasöründe).

Daha eski bir çalıştırmanın tek bir alt klasörde bıraktığı `.cpwo-output`, projenin
kaydına katılır ve silinir. CPWO'ya ait hiçbir şey kalmadığında `.cpwo-output`
dosyasının kendisi de silinir.

### CPWO'nun dokunmadığı dosyalar

CPWO her çalıştırmanın başında `.cpwo-output` dosyasını okur ve yalnızca orada ya da
dizin ağacında daha yukarıdaki bir `.cpwo-output` içinde listelenen bir dosyanın
üzerine yazar veya onu siler. Çıktının gideceği yerde duran başka her şeye
dokunulmaz: `bootstrap.css` dosyasının yanındaki, kütüphanenin kendi
`bootstrap.min.css` dosyası ya da birinin elle dışa aktardığı bir `.webp`.

İki kaynak aynı çıktıyı isteyebilir; örneğin `photo.jpg` ile `photo.png` ikisi de
`photo.webp` ister. Ad sırasına göre ilk gelen onu alır, her çalıştırmada aynı
şekilde; diğerine dokunulmaz.

Özet satırı bunları sayar (`N left alone`); `-v` her birinin adını yazar ve nedenini
söyler. Çıktılar var ama onları listeleyecek bir `.cpwo-output` yoksa (silinmiş ya
da hiç commit edilmemiş), CPWO bunlara dokunmaz ve kaç tane olduğunu söyleyip
`-adopt` seçeneğini gösteren bir ipucu yazdırır. `-adopt` ile bir kez çalıştırmak
bunları devralır ve bundan sonra kayıt onları listeler.

### Git

`.cpwo-output` dosyasını ve çıktıları birlikte tutun: ya ikisi de commit edilsin ya
da ikisi de yok sayılsın. Klonladıktan sonra, kayıt dosyası olmadan commit edilmiş
çıktılara sonraki çalıştırma dokunmaz ve `-adopt` seçeneğini gösteren bir ipucu
yazdırır. `-adopt` ile bir kez çalıştırmak bunları devralır.

Bunları yok sayacaksanız, zaten küçültülmüş olarak gelen dosyaları yok saymaktan
muaf tutun, çünkü `*.min.*` onlarla da eşleşir. Kendi ürettiğiniz `.webp` ya da
`.avif` dosyaları için de aynısını yapın:

```gitignore
.cpwo-output
*.min.*
*.webp
*.avif
!bootstrap.min.css
!bootstrap.bundle.min.js
```

## Koruma önlemleri

Optimizasyon bir dosyayı büyütüyorsa CPWO orijinali korur. Bu, beklediğinizden sık
olur: `-q 82` değerinde düz renkli bir logonun kayıplı WebP'si çoğu zaman kaynak
PNG'den büyük çıkar. Bu yüzden PNG kaynaklar hem kayıplı hem kayıpsız kodlanır,
küçük olan kazanır ve o da kaynağı geçemiyorsa hiç `.webp` yazılmaz.

Aynı kural AVIF için bir adım daha ileri gider. `<picture>` önce `.avif` dosyasını
önerdiği için bir `.avif`, yerini ancak hem kaynağı hem `.webp` dosyasını geçerse
hak eder. Gerçek bir logo setinden:

```
logo-512.png  23,799  ->  logo-512.webp   9,996  ->  logo-512.avif  5,881
logo-256.png  10,782  ->  logo-256.webp   8,586  ->  logo-256.avif  2,912
logo-32.png    2,667  ->  logo-32.webp      656  ->  no .avif, AVIF came out 756
```

Küçültme aracı bir dosyanın sözdizimini kabul etmezse CPWO dosyayı kaybetmek yerine
kopyalar.

Her dosya geçici bir dosyaya yazılır ve adı değiştirilerek yerine konur; böylece
yarıda kesilen bir çalıştırma, sonraki çalıştırmaların güncel sayacağı yarım bir
dosya bırakmaz.

Genişliği ya da yüksekliği 16383 px'ten büyük bir görsel için `.webp` yazılmaz,
çünkü biçimin sınırı budur. CPWO bir uyarı yazdırır ve devam eder.

Görseller yeniden kodlanmadan önce doğru yöne çevrilir; bir `<picture>`
içinde yan yatmış bir WebP ile dik duran bir JPEG yan yana çıkamaz. Bkz.
[EXIF](#exif).

HTML'de varsayılan öznitelik değerleri korunur. Bir `<input>` etiketinden
`type="text"` ifadesini atmak geçerli HTML'dir ve birkaç bayt kazandırır, ama
`input[type="text"]` kullanan her CSS kuralının ve `querySelector` çağrısının
eşleşmesini de bozar. Sayfa yine görüntülenir, yalnızca yanlış görünür. CPWO bu
takasa girmez.

## Hangi dosyaya ne olur

| Uzantı | İşlem |
|---|---|
| `.html .htm` | Küçültülür; istenirse `-relink` ve `-picture` de uygulanır |
| `.css .js .mjs .json .svg .xml` | Küçültülür |
| `.jpg .jpeg .png` | `-q` değeriyle yeniden sıkıştırılır; yanına bir `.webp`, `-avif` verilirse bir `.avif` de yazılır |
| `*.min.*`, `*.webp`, `*.avif` | Atlanır: ya zaten küçültülmüştür ya da CPWO'nun kendi işidir |
| başka her şey | Dokunulmaz |

Orijinaller yerinde kalır, bu sayede `<picture>` fallback'leri çalışmaya devam eder;
`-picture` da sizin için bunu yazar.

## Bağımlılıklar

Hepsi saf Go.

| Modül | Lisans | Kullanım amacı |
|---|---|---|
| [tdewolff/minify/v2](https://github.com/tdewolff/minify) | MIT | HTML, CSS, JS, JSON, SVG ve XML küçültme |
| [gen2brain/webp](https://github.com/gen2brain/webp) | MIT. libwebp 1.6.0'ı (BSD-3-Clause) içerir | WebP kodlama, kayıplı ve kayıpsız |
| [gen2brain/avif](https://github.com/gen2brain/avif) | MIT. libavif 1.4.2'yi (BSD-2-Clause) aom 3.14.1 (BSD-2-Clause), dav1d ve libyuv ile birlikte içerir | AVIF kodlama |
| [golang.org/x/net/html](https://pkg.go.dev/golang.org/x/net/html) | BSD-3-Clause | `-relink` ve `-picture` için HTML ayrıştırma |
| [golang.org/x/image/draw](https://pkg.go.dev/golang.org/x/image/draw) | BSD-3-Clause | `-r` için aşağı ölçekleme |
| [tdewolff/parse/v2](https://github.com/tdewolff/parse) | MIT | Küçültme araçlarının arkasındaki ayrıştırıcılar, dolaylı bağımlılık olarak gelir |
| [tetratelabs/wazero](https://github.com/tetratelabs/wazero) | Apache-2.0 | AVIF kodlayıcısının WebAssembly'sini çalıştırır, dolaylı bağımlılık olarak gelir |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | BSD-3-Clause | CPU algılama ve sistem çağrıları, dolaylı bağımlılık olarak gelir |
| [ebitengine/purego](https://github.com/ebitengine/purego) | Apache-2.0 | Kodlayıcılar tarafından çağrılır, dolaylı bağımlılık olarak gelir. Release derlemelerinde yoktur: yalnızca `-tags nodynamic` olmadan bağlanır |
| [goreleaser](https://github.com/goreleaser/goreleaser) | Dağıtılmaz | Yalnızca derleme zamanında, release çıkarmak için |

Geri kalan her şey Go standart kütüphanesidir (BSD-3-Clause). Lisans metinlerinin
tamamı [licenses.md](licenses.md) dosyasındadır, kısa halleri ise [Lisans](#lisans)
bölümündedir.

## Bağımlılıkları güncelleme

`scripts/deps.sh` her Go modülünü mevcut ve en son sürümüyle, bir de deposuyla
listeler; ardından kodlayıcıların içine derlenmiş C kütüphanelerini, Go araç
zincirini ve kullanılan GitHub Action'larını sıralar, hangilerinin güncellemesi
olduğunu da söyler.

```sh
scripts/deps.sh             # what has an update
scripts/deps.sh --apply     # update the outdated modules, go mod tidy, run the tests
scripts/deps.sh licenses    # regenerate licenses.md
```

`licenses` komutu [licenses.md](licenses.md) dosyasını yeniden yazar. Her bağımlılık
güncellemesinden sonra çalıştırın, çünkü çalıştırmazsanız bir test başarısız olur.
Betik için bash ve curl gerekir; `--apply` ve `licenses` için ayrıca Go da gerekir.
`GITHUB_TOKEN` tanımlarsanız GitHub'ın saatte 60 anonim API çağrısı sınırına
takılmazsınız.

## Derleme

```sh
go mod tidy
go test -tags nodynamic ./...
go build ./...
```

`go test -tags nodynamic ./...` komutunun geçmesi gerekir. Bu etiket olmadan
kodlayıcılar, kuruluysa sistemdeki libwebp/libavif'i kullanır; bu durumda testler,
yayınlanan kodlayıcıdan farklı bir kodlayıcıyı ölçmüş olur. Ardından
`scripts/build.sh` altı hedefin hepsini `bin/<os>-<arch>/` altına çapraz derler
(örneğin `bin/linux-amd64/cpwo` ve `bin/windows-amd64/cpwo.exe`). Ayrıca
`bin/SHA256SUMS` dosyasını yazar ve `file` kuruluysa Linux binary'lerinin statik
olduğunu kontrol eder.

```sh
scripts/build.sh                             # all six targets
scripts/build.sh linux/amd64 windows/amd64   # only these
VERSION=1.2.3 scripts/build.sh               # stamp a version
```

Sürüm varsayılan olarak git tag'inden alınır, tag yoksa `main.go` içindeki sürüm
kullanılır.

Release derlemeleri `-tags nodynamic` kullanır. Bu olmadan WebP ve AVIF paketleri
başlangıçta sistemde bir libwebp/libavif arar ve Linux binary'si statik olmaktan
çıkar. Hızlı bir yerel derleme için:

```sh
CGO_ENABLED=0 go build -tags nodynamic .
```

GitHub'da her push ve pull request, testleri Linux, Windows ve macOS üzerinde
çalıştırır, ardından altı hedefin hepsini derler. Binary'ler workflow
çalıştırmasına `cpwo-<commit>` adlı bir artifact olarak eklenir. Bir `vX.Y.Z` tag'i
push'lanınca testler çalışır ve GoReleaser ile bir release çıkarılır; GoReleaser
aynı altı hedefi derler.

## Lisans

CPWO, MIT Lisansı altında dağıtılır. Copyright (c) 2026 CyClone
(https://cyclone9.dev). Tam metin [LICENSE](LICENSE) dosyasındadır.

MIT Lisansı CPWO'nun kodunu ve belgelerini kapsar. CPWO logosunu, kapak görsellerini ve [assets/](assets/) klasöründeki videoları kapsamaz; bunlar CyClone'a aittir ve tüm hakları saklıdır. Videolardaki müzik üçüncü bir taraftan ayrıca lisanslanmıştır ve CPWO'nun parçası değildir.

Bir CPWO binary'si MIT, BSD ve Apache-2.0 lisanslı üçüncü taraf kod da içerir.
[licenses.md](licenses.md) her lisans metnini aynen içerir. `cpwo -license` ise
CPWO'nun lisansını (MIT) telif hakkı satırıyla ve her bileşen için bir satır (ad,
lisans, CPWO'nun onu ne için kullandığı) yazdırır, ardından tam metinlerin
licenses.md dosyasında olduğunu söyler. Release arşivlerine LICENSE, licenses.md
ve README dosyaları konur; `scripts/build.sh` yerel derlemede LICENSE ile
licenses.md dosyalarını `bin/` içine, binary'lerin yanına kopyalar.

### Lisanslar sade bir dille

[Bağımlılıklar](#bağımlılıklar) tablosundaki lisansların kısa halleri. Hepsinde kod
olduğu gibi, garantisiz gelir; geçerli olan, [licenses.md](licenses.md) içindeki tam
metinlerdir.

| Lisans | Yapabilecekleriniz | Uymanız gerekenler |
|---|---|---|
| MIT | Kodu kullanmak, kopyalamak, değiştirmek, yayımlamak, dağıtmak ve satmak | Telif hakkı bildirimini ve lisans metnini her kopyayla birlikte korumak |
| BSD-2-Clause | Kodu değiştirerek ya da değiştirmeden kullanmak ve yeniden dağıtmak | Telif hakkı bildirimini, lisans metnini ve sorumluluk reddini kaynakta ve binary ile gelen belgelerde korumak |
| BSD-3-Clause | BSD-2-Clause ile aynı | BSD-2-Clause ile aynı; ayrıca yazarların adlarını, yazılı izin almadan, koddan geliştirilen bir ürünü onaylamak ya da tanıtmak için kullanmamak |
| Apache-2.0 | Kodu kullanmak, değiştirmek ve yeniden dağıtmak. Katkıda bulunanlar, patentleri için de size bir lisans verir | Lisansı, telif hakkı bildirimlerini ve NOTICE dosyasını korumak ve değiştirdiğiniz dosyaları işaretlemek. Koddaki bir patent yüzünden dava açarsanız patent lisansı sizin için sona erer. Lisans, ticari marka kullanma hakkı vermez |
