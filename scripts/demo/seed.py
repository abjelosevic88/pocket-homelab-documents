import json, subprocess, time, urllib.request, http.cookiejar
B = "http://127.0.0.1:18190/api/v1"
cj = http.cookiejar.CookieJar(); op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj))
def req(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(B + path, data=data, method=method, headers={"Content-Type": "application/json"})
    with op.open(r) as res:
        raw = res.read()
        return json.loads(raw) if raw else None
req("POST", "/auth/login", {"email": "ana@example.com", "password": "screenshots-2026"})
cookie = [c for c in cj if c.name == "pd_session"][0].value
open("cookie", "w").write(cookie)

tags = {}
for name, color, alg, match in [("Bills", "#1f78b4", 0, ""), ("Tax", "#6a3d9a", 1, "porez порез tax"), ("Home", "#33a02c", 6, ""), ("Car", "#ff7f00", 1, "vozilo vozila škoda"), ("Health", "#e31a1c", 1, "налаз nalaz здравља"),
                                 ("Insurance", "#b15928", 1, "polisa osiguranje"), ("Warranty", "#0f766e", 1, "garantie garancija warranty"), ("Paid", "#64748b", 3, "PLAĆENO"), ("Servers", "#334155", 1, "hetzner")]:
    tags[name] = req("POST", "/tags", {"name": name, "color": color, "matching_algorithm": alg, "match": match})["id"]
inbox = [t for t in req("GET", "/tags") if t["is_inbox"]][0]["id"]
corr = {}
for name, match in [("Elektro Banja Luka", "elektro banja luka"), ("m:tel", "m:tel"), ("Raiffeisen Bank", "raiffeisen"), ("Generali Osiguranje", "generali"), ("Nekretnine Vrbas", "nekretnine vrbas"),
                    ("Hetzner", "hetzner"), ("Bosch", "bosch"), ("Auto Centar Krajina", "auto centar krajina"), ("Пореска управа", "пореска управа"), ("Дом здравља", "дом здравља"), ("Grad Banja Luka", "opština banja luka gradska uprava")]:
    corr[name] = req("POST", "/correspondents", {"name": name, "match": match, "matching_algorithm": 3, "is_insensitive": True})["id"]
types = {}
for name, match in [("Invoice", "račun invoice"), ("Statement", "izvod"), ("Contract", "ugovor"), ("Policy", "polisa"), ("Decision", "рјешење"), ("Medical report", "налаз"), ("Warranty", "garantiekarte"), ("Letter", "obavještenje")]:
    types[name] = req("POST", "/document_types", {"name": name, "match": match, "matching_algorithm": 1})["id"]
paths = {}
paths["By year"] = req("POST", "/storage_paths", {"name": "By year and correspondent", "path": "{created_year}/{correspondent}/{created} {title}", "matching_algorithm": 0})["id"]
paths["Car"] = req("POST", "/storage_paths", {"name": "Car", "path": "Car/{created_year}/{title}", "matching_algorithm": 1, "match": "škoda vozila"})["id"]
amount = req("POST", "/custom_fields", {"name": "Amount", "data_type": "monetary", "extra": {"currency": "BAM"}})["id"]
due = req("POST", "/custom_fields", {"name": "Due date", "data_type": "date"})["id"]
expires = req("POST", "/custom_fields", {"name": "Expires", "data_type": "date"})["id"]
req("POST", "/rules", {"name": "Bills get the Bills tag and a 30-day reminder", "enabled": True, "trigger": "added", "filters": {"filename": "", "path": "", "source": "", "mime": "", "content": "rok plaćanja", "tags": []},
    "actions": {"title": "", "add_tags": [tags["Bills"]], "remove_tags": [], "fields": {}}})
req("POST", "/rules", {"name": "Insurance and warranties: remind 30 days before they end", "enabled": True, "trigger": "scheduled", "filters": {"schedule_field": f"cf:{expires}", "schedule_offset_days": -30, "tags": []},
    "actions": {"title": "", "add_tags": [], "remove_tags": [], "fields": {}, "notify": True, "notify_message": "{title} ends in 30 days"}})
req("POST", "/rules", {"name": "IKEA receipts", "enabled": True, "trigger": "added", "filters": {"filename": "*ikea*", "tags": []}, "actions": {"title": "IKEA {created}", "add_tags": [tags["Home"]], "remove_tags": [], "fields": {}}})

def up(fn, **meta):
    args = ["curl", "-s", "-b", f"pd_session={cookie}", "-F", f"document=@pdf/{fn}"]
    for k, v in meta.items():
        args += ["-F", f"{k}={v}"]
    out = json.loads(subprocess.check_output(args + [B + "/documents"]))
    return out["id"]
ids = {}
ids["mart"] = up("2026-03-12_elektro_mart.pdf", title="Električna energija — mart 2026")
ids["april"] = up("2026-04-11_elektro_april.pdf", title="Električna energija — april 2026")
ids["maj"] = up("2026-05-12_elektro_maj.pdf", title="Električna energija — maj 2026")
ids["mtel"] = up("2026-04-03_mtel_racun.pdf", title="m:tel račun — mart 2026")
ids["izvod"] = up("2026-04-02_raiffeisen_izvod.pdf", title="Izvod — mart 2026")
ids["polisa"] = up("2026-03-21_generali_polisa.pdf", title="Polisa autoodgovornosti Škoda Fabia")
ids["lease"] = up("lease_contract_2025.pdf", title="Ugovor o zakupu stana", created="2025-09-01")
ids["hetzner"] = up("hetzner_invoice_R0021552.pdf", title="Hetzner invoice R0021552711", created="2026-04-01")
ids["bosch"] = up("bosch_garantiekarte.pdf", title="Garantiekarte Waschmaschine Serie 6", created="2025-11-18")
ids["servis"] = up("2026-02-18_servis_skoda.pdf", title="Mali servis — 48.212 km")
ids["porez"] = up("2025-12-15_godisnji_porez.jpg")
ids["nalaz"] = up("nalaz_dom_zdravlja.jpg")
ids["obav"] = up("obavjestenje_odvoz_otpada.jpg")
open("ids.json", "w").write(json.dumps({"ids": ids, "tags": tags, "corr": corr, "types": types, "fields": {"amount": amount, "due": due, "expires": expires}, "inbox": inbox}))
print("uploaded", len(ids))
