import json, urllib.request
B = "http://127.0.0.1:18190/api/v1"; C = open("cookie").read().strip()
meta = json.load(open("ids.json")); ids, tags, corr, types, F = meta["ids"], meta["tags"], meta["corr"], meta["types"], meta["fields"]
def req(method, path, body=None):
    r = urllib.request.Request(B + path, data=json.dumps(body).encode() if body is not None else None, method=method, headers={"Content-Type": "application/json", "Cookie": "pd_session=" + C})
    with urllib.request.urlopen(r) as res:
        raw = res.read(); return json.loads(raw) if raw else None
def fields(d, **kv):
    return [{"field": F[k], "value": v} for k, v in kv.items()]
up = lambda k, body: req("PUT", f"/documents/{ids[k]}", body)
up("mart", {"custom_fields": fields(None, amount="58.40", due="2026-03-31"), "asn": 101})
up("april", {"custom_fields": fields(None, amount="53.10", due="2026-04-30"), "asn": 102, "favorite": True})
up("maj", {"custom_fields": fields(None, amount="49.85", due="2026-05-31"), "asn": 103})
up("mtel", {"custom_fields": fields(None, amount="89.80", due="2026-04-25"), "asn": 104})
up("izvod", {"correspondent": corr["Raiffeisen Bank"], "document_type": types["Statement"], "asn": 105, "tags": [tags["Bills"]]})
up("polisa", {"custom_fields": fields(None, amount="412.00", expires="2027-03-21"), "asn": 106, "favorite": True})
up("lease", {"custom_fields": fields(None, expires="2026-08-31"), "asn": 107, "tags": [tags["Home"]], "favorite": True})
up("hetzner", {"custom_fields": fields(None, amount="9.01"), "tags": [tags["Servers"], tags["Paid"]]})
up("bosch", {"custom_fields": fields(None, expires="2027-11-18"), "tags": [tags["Warranty"], tags["Home"]], "asn": 108})
up("servis", {"custom_fields": fields(None, amount="269.50"), "storage_path": None, "asn": 109})
up("nalaz", {"title": "Систематски преглед — налаз", "correspondent": corr["Дом здравља"], "document_type": types["Medical report"], "tags": [tags["Health"]], "created": "2026-03-03"})
# everything filed leaves the inbox; the two Cyrillic/Latin scans stay to show suggestions
req("POST", "/documents/bulk", {"ids": [v for k, v in ids.items() if k not in ("porez", "obav")], "op": "remove_inbox"})
req("POST", f"/documents/{ids['polisa']}/notes", {"text": "Plaćeno karticom 21.03. Produžiti do 21.03.2027 — Generali šalje ponudu mjesec dana ranije."})
req("POST", f"/documents/{ids['lease']}/notes", {"text": "Depozit 1.300 KM vraća se po isteku ugovora. Otkazni rok 3 mjeseca."})
req("POST", f"/documents/{ids['lease']}/notes", {"text": "Zakupodavac pristao na produženje do avgusta 2027 (mail od 2.10.)."})
req("POST", "/saved_views", {"name": "Bills to pay", "query": {"tags": str(tags["Bills"]), "sort": "-created"}, "on_dashboard": True, "in_sidebar": True})
req("POST", "/saved_views", {"name": "Expiring this year", "query": {"field": f"{F['expires']}", "sort": "created"}, "on_dashboard": False, "in_sidebar": True})
req("POST", "/saved_views", {"name": "Serbian (Cyrillic)", "query": {"q": "Бања Лука"}, "on_dashboard": False, "in_sidebar": True})
req("PUT", "/folder", {"kind": "folder", "path": "/data/mirror", "file": "archive", "inbox": True, "inbox_name": "Inbox"})
acc = req("POST", "/mail/accounts", {"name": "Personal mail", "host": "imap.example.com", "port": 993, "security": "ssl", "username": "ana@example.com", "password": "not-a-real-one", "poll_minutes": 10, "enabled": False})
req("POST", "/mail/rules", {"account_id": acc["id"], "name": "Invoices from the bills folder", "folder": "Bills", "filter_attachment": "*.pdf", "consume": "attachments", "title_from": "subject", "assign_tags": [tags["Bills"]], "correspondent_from": "name", "action": "mark_read", "only_unseen": True})
req("POST", "/mail/rules", {"account_id": acc["id"], "name": "Hetzner invoices", "filter_from": "billing@hetzner.com", "consume": "attachments", "title_from": "filename", "assign_tags": [tags["Servers"]], "assign_correspondent": corr["Hetzner"], "action": "move", "action_folder": "Archive/Hetzner"})
req("PUT", "/settings", {"barcodes_enabled": True, "date_format": "dmy-dot"})
print("ok")
