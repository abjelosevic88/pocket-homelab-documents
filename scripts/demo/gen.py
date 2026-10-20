# Demo documents for the README screenshots. All names, numbers and companies are made up.
import os
CSS = """
@page { size: A4; margin: 0; }
body { font-family: 'DejaVu Sans', Arial, sans-serif; color: #1f2937; margin: 0; padding: 18mm 18mm 14mm; font-size: 10.5pt; line-height: 1.45; }
.head { display: flex; justify-content: space-between; align-items: flex-start; border-bottom: 3px solid VAR; padding-bottom: 10px; margin-bottom: 18px; }
.logo { font-weight: 800; font-size: 20pt; color: VAR; letter-spacing: -.5px; }
.logo small { display: block; font-size: 8.5pt; font-weight: 400; color: #6b7280; letter-spacing: 0; }
.meta { text-align: right; font-size: 9pt; color: #4b5563; }
h1 { font-size: 15pt; margin: 6px 0 14px; color: #111827; }
.addr { display: flex; justify-content: space-between; margin-bottom: 18px; font-size: 9.5pt; }
.box { border: 1px solid #e5e7eb; border-radius: 6px; padding: 8px 12px; min-width: 70mm; }
table { width: 100%; border-collapse: collapse; margin: 10px 0 14px; font-size: 9.5pt; }
th { background: #f3f4f6; text-align: left; padding: 6px 8px; border-bottom: 1px solid #d1d5db; }
td { padding: 6px 8px; border-bottom: 1px solid #eef0f3; }
td.r, th.r { text-align: right; }
.total { font-size: 12pt; font-weight: 700; text-align: right; margin-top: 6px; }
.note { font-size: 8.5pt; color: #6b7280; margin-top: 22px; border-top: 1px solid #e5e7eb; padding-top: 8px; }
.stamp { display: inline-block; border: 2px solid #b91c1c; color: #b91c1c; padding: 4px 10px; transform: rotate(-6deg); font-weight: 700; margin-top: 14px; }
p { margin: 0 0 8px; }
"""
def page(color, logo, sub, meta, title, body):
    return f"<html><head><meta charset='utf-8'><style>{CSS.replace('VAR', color)}</style></head><body><div class='head'><div class='logo'>{logo}<small>{sub}</small></div><div class='meta'>{meta}</div></div><h1>{title}</h1>{body}</body></html>"

def rows(items, cur):
    return "".join(f"<tr><td>{a}</td><td class='r'>{b}</td><td class='r'>{c} {cur}</td></tr>" for a, b, c in items)

docs = {}
def energy(month, mnum, kwh, amount, inv, date, due):
    return page("#0e7490", "ELEKTRO BANJA LUKA", "a.d. · Kralja Petra I Karađorđevića 95, 78000 Banja Luka",
        f"Račun br. <b>{inv}</b><br>Datum: {date}<br>Kupac br. 4417-2290", f"Račun za električnu energiju — {month} 2026",
        f"""<div class='addr'><div class='box'><b>Ana Petrović</b><br>Ulica Jovana Dučića 18<br>78000 Banja Luka</div><div class='box'>Obračunski period<br><b>01.{mnum}.2026 – 30.{mnum}.2026</b><br>Brojilo: 99-114-2083</div></div>
<table><tr><th>Stavka</th><th class='r'>Količina</th><th class='r'>Iznos</th></tr>{rows([("Viša tarifa (VT)", f"{int(kwh*0.7)} kWh", f"{amount*0.62:.2f}".replace('.',',')), ("Niža tarifa (NT)", f"{int(kwh*0.3)} kWh", f"{amount*0.18:.2f}".replace('.',',')), ("Mjesečna naknada za mjerno mjesto", "1", "4,20"), ("PDV 17%", "", f"{amount*0.145:.2f}".replace('.',','))], "KM")}</table>
<div class='total'>Ukupno za uplatu: {f"{amount:.2f}".replace('.',',')} KM</div><p>Rok plaćanja: <b>{due}</b>. Uplatu izvršite na račun 562-099-00001234-56 sa pozivom na broj {inv}.</p>
<div class='note'>Hvala što plaćate na vrijeme. Reklamacije na račun primamo u roku od 8 dana od dana prijema. Kontakt centar 051 246 300.</div>""")
docs["2026-03-12_elektro_mart.pdf"] = energy("mart", "03", 412, 58.40, "2026-03-1187", "12.03.2026", "31.03.2026")
docs["2026-04-11_elektro_april.pdf"] = energy("april", "04", 377, 53.10, "2026-04-2231", "11.04.2026", "30.04.2026")
docs["2026-05-12_elektro_maj.pdf"] = energy("maj", "05", 341, 49.85, "2026-05-0904", "12.05.2026", "31.05.2026")

docs["2026-04-03_mtel_racun.pdf"] = page("#1d4ed8", "m:tel", "Mobilne i fiksne usluge · Vuka Karadžića 2, Banja Luka", "Broj računa <b>MT-88-219044</b><br>Datum izdavanja: 03.04.2026", "Račun za telekomunikacione usluge — mart 2026",
    f"""<div class='addr'><div class='box'><b>Ana Petrović</b><br>Ulica Jovana Dučića 18<br>78000 Banja Luka</div><div class='box'>Korisnički broj: 065-114-220<br>Paket: <b>Familija Max</b></div></div>
<table><tr><th>Usluga</th><th class='r'>Period</th><th class='r'>Iznos</th></tr>{rows([("Pretplata Familija Max", "03/2026", "39,90"), ("Internet 300/30 Mbit/s", "03/2026", "29,90"), ("m:tel TV Extra", "03/2026", "12,00"), ("Roaming EU (paket 5 GB)", "14.–19.03.", "8,00")], "KM")}</table>
<div class='total'>Ukupno: 89,80 KM</div><p>Rok plaćanja: 25.04.2026.</p><div class='note'>Obavještenje: od 1. maja 2026. mijenjaju se uslovi paketa Familija. Detalji na mtel.ba/uslovi.</div>""")

docs["2026-04-02_raiffeisen_izvod.pdf"] = page("#ca8a04", "Raiffeisen BANK", "Raiffeisen Bank d.d. Bosna i Hercegovina", "Izvod br. <b>3/2026</b><br>Datum: 02.04.2026<br>IBAN BA39 1610 0000 1234 5678", "Izvod sa tekućeg računa — mart 2026",
    f"""<table><tr><th>Datum</th><th>Opis</th><th class='r'>Iznos</th></tr>
<tr><td>01.03.</td><td>Plata — Studio Atelje d.o.o.</td><td class='r'>+3.420,00 KM</td></tr><tr><td>02.03.</td><td>Zakup stana — Nekretnine Vrbas</td><td class='r'>−650,00 KM</td></tr>
<tr><td>05.03.</td><td>Konzum Banja Luka</td><td class='r'>−84,37 KM</td></tr><tr><td>12.03.</td><td>Elektro Banja Luka, račun 2026-03-1187</td><td class='r'>−58,40 KM</td></tr>
<tr><td>14.03.</td><td>IKEA Beograd (kartica)</td><td class='r'>−312,90 KM</td></tr><tr><td>21.03.</td><td>Generali Osiguranje, polisa AO-551209</td><td class='r'>−412,00 KM</td></tr>
<tr><td>28.03.</td><td>m:tel, račun MT-88-208311</td><td class='r'>−89,80 KM</td></tr></table>
<p>Početno stanje: 2.104,55 KM · Završno stanje: <b>3.917,08 KM</b></p><div class='note'>Izvod je izrađen elektronski i važi bez potpisa i pečata.</div>""")

docs["2026-03-21_generali_polisa.pdf"] = page("#b91c1c", "GENERALI", "Generali Osiguranje d.d. · Banja Luka", "Polisa br. <b>AO-551209</b><br>Datum: 21.03.2026", "Polisa obaveznog osiguranja od autoodgovornosti",
    """<div class='addr'><div class='box'><b>Osiguranik</b><br>Ana Petrović<br>Ulica Jovana Dučića 18, Banja Luka</div><div class='box'><b>Vozilo</b><br>Škoda Fabia 1.0 TSI<br>Reg. oznaka: M12-K-345<br>Broj šasije: TMBEP6NJ5MZ012345</div></div>
<table><tr><th>Pokriće</th><th class='r'>Suma</th><th class='r'>Premija</th></tr><tr><td>Autoodgovornost</td><td class='r'>po zakonu</td><td class='r'>356,00 KM</td></tr><tr><td>Nezgoda vozača i putnika</td><td class='r'>20.000 KM</td><td class='r'>56,00 KM</td></tr></table>
<div class='total'>Ukupna premija: 412,00 KM</div><p>Trajanje osiguranja: od <b>21.03.2026</b> do <b>21.03.2027</b> u 24:00 časa.</p><div class='stamp'>PLAĆENO</div>""")

docs["lease_contract_2025.pdf"] = page("#334155", "NEKRETNINE VRBAS", "d.o.o. · agencija za promet nekretninama", "Ugovor br. <b>NV-2025-118</b>", "Ugovor o zakupu stana",
    """<p>Zaključen dana <b>1. septembra 2025.</b> u Banjoj Luci između:</p><p><b>Zakupodavca</b>: Milan Kovačević, JMBG 1203975100011, Banja Luka, i</p><p><b>Zakupca</b>: Ana Petrović, Banja Luka.</p>
<p><b>Član 1.</b> Predmet zakupa je dvosoban stan površine 58 m², Ulica Jovana Dučića 18, sprat III, stan br. 9.</p><p><b>Član 2.</b> Mjesečna zakupnina iznosi 650,00 KM i plaća se do 5. u mjesecu. Depozit iznosi 1.300,00 KM.</p>
<p><b>Član 3.</b> Ugovor se zaključuje na određeno vrijeme od 12 mjeseci, do 31. avgusta 2026. Otkazni rok je tri mjeseca.</p><p><b>Član 4.</b> Troškove električne energije, vode, grijanja i komunalija snosi zakupac.</p>
<p><b>Član 5.</b> Za sporove je nadležan Osnovni sud u Banjoj Luci.</p><br><p>Zakupodavac ______________________ &nbsp;&nbsp;&nbsp;&nbsp; Zakupac ______________________</p>""")

docs["hetzner_invoice_R0021552.pdf"] = page("#d50c2d", "HETZNER", "Hetzner Online GmbH · Industriestr. 25 · 91710 Gunzenhausen · Germany", "Invoice <b>R0021552711</b><br>Date: 01/04/2026<br>Customer: K0912204", "Invoice",
    f"""<div class='addr'><div class='box'>Ana Petrović<br>Jovana Dučića 18<br>78000 Banja Luka<br>Bosnia and Herzegovina</div></div>
<table><tr><th>Product</th><th class='r'>Period</th><th class='r'>Amount</th></tr>{rows([("Storage Box BX11 (1 TB)", "04/2026", "3.81"), ("Cloud Server CX22 #48812", "04/2026", "4.59"), ("Primary IPv4", "04/2026", "0.61")], "EUR")}</table>
<div class='total'>Total: 9.01 EUR</div><p>The amount will be charged to your credit card ending in 4417.</p><div class='note'>Reverse charge: VAT to be paid by the recipient.</div>""")

docs["bosch_garantiekarte.pdf"] = page("#ea0016", "BOSCH", "Robert Bosch Hausgeräte GmbH", "Garantiekarte<br>Kaufdatum: 18.11.2025", "Garantiekarte — Waschmaschine Serie 6",
    """<table><tr><th>Gerät</th><th>Modell</th><th>Seriennummer</th></tr><tr><td>Waschmaschine, Frontlader</td><td>WGG244A0BY</td><td>FD 0412 00988</td></tr></table>
<p>Die Garantiezeit beträgt <b>24 Monate</b> ab Kaufdatum und gilt bis zum <b>18.11.2027</b>. Bitte bewahren Sie diese Karte zusammen mit dem Kaufbeleg auf.</p>
<p>Händler: Tehnomarket Banja Luka, Rechnung 11-2025-4471, Preis 1.149,00 KM.</p><div class='note'>Kundendienst: +49 89 69 339 339 · bosch-home.com/service</div>""")

docs["2026-02-18_servis_skoda.pdf"] = page("#15803d", "AUTO CENTAR KRAJINA", "Ovlašteni servis Škoda · Banja Luka", "Radni nalog <b>RN-2026-0388</b><br>Datum: 18.02.2026", "Račun — redovni servis vozila",
    f"""<div class='addr'><div class='box'>Vozilo: Škoda Fabia, M12-K-345<br>Kilometraža: <b>48.212 km</b></div></div>
<table><tr><th>Rad / dio</th><th class='r'>Kol.</th><th class='r'>Iznos</th></tr>{rows([("Ulje motora 5W-30", "4 l", "96,00"), ("Filter ulja", "1", "24,50"), ("Filter vazduha", "1", "31,00"), ("Filter kabine", "1", "28,00"), ("Rad — mali servis", "1,5 h", "90,00")], "KM")}</table>
<div class='total'>Ukupno sa PDV: 269,50 KM</div><p>Sljedeći servis: na 63.000 km ili do 18.02.2027.</p>""")

docs["telekom_obavjestenje.pdf"] = page("#7c3aed", "OPŠTINA BANJA LUKA", "Gradska uprava · Odjeljenje za komunalne poslove", "Broj: 06-372-1188/26<br>Datum: 9. januar 2026.", "Obavještenje o odvozu kabastog otpada",
    """<p>Poštovani građani,</p><p>obavještavamo vas da će se odvoz kabastog otpada u Vašoj mjesnoj zajednici obaviti <b>14. februara 2026.</b> od 8 do 14 časova.</p><p>Otpad odložite pored kontejnera najranije dan ranije. Ne odlažite građevinski otpad, gume ni elektronski otpad.</p><p>S poštovanjem,<br>Odjeljenje za komunalne poslove</p>""")

docs["2025-12-15_godisnji_porez.pdf"] = page("#0f766e", "ПОРЕСКА УПРАВА", "Република Српска · Подручни центар Бања Лука", "Број: 06/1.07/0702-455-31/25<br>Датум: 15.12.2025.", "Рјешење о утврђивању пореза на непокретности за 2025. годину",
    """<p>Пореском обвезнику <b>Ана Петровић</b>, Бања Лука, утврђује се порез на непокретности за 2025. годину у износу од <b>146,80 КМ</b>.</p>
<table><tr><th>Непокретност</th><th class='r'>Вриједност</th><th class='r'>Порез</th></tr><tr><td>Стан, Улица Јована Дучића 18, 58 m²</td><td class='r'>73.400,00 КМ</td><td class='r'>146,80 КМ</td></tr></table>
<p>Порез се плаћа у двије једнаке рате, до 30. јуна и до 30. септембра 2026. године, на рачун јавних прихода.</p><p>Против овог рјешења може се изјавити жалба у року од 15 дана од дана пријема.</p><div class='stamp' style='border-color:#1e3a8a;color:#1e3a8a'>ПОРЕСКА УПРАВА</div>""")

docs["nalaz_dom_zdravlja.pdf"] = page("#0369a1", "ДОМ ЗДРАВЉА БАЊА ЛУКА", "Служба опште медицине · Амбуланта Лауш", "Протокол бр. 2210/26<br>Датум: 3. марта 2026.", "Љекарски налаз и мишљење",
    """<p>Пацијент: <b>Ана Петровић</b>, рођена 1990.</p><p><b>Анамнеза:</b> систематски преглед. Без тегоба.</p><p><b>Налаз:</b> Крвни притисак 118/76 mmHg, пулс 68/min. Лабораторијски налази у границама референтних вриједности. Холестерол 4,9 mmol/L.</p><p><b>Мишљење:</b> Здрава. Контрола за годину дана.</p><br><p>Доктор медицине<br>др Јелена Марковић, спец. опште медицине</p>""")

for name, html in docs.items():
    open(os.path.join("src", name.replace(".pdf", ".html")), "w").write(html)
print(len(docs), "html files")
