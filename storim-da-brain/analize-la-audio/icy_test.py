import urllib.request

url = "http://edge126.rdsnet.ro:84/profm/profm.mp3"
req = urllib.request.Request(url, headers={"Icy-MetaData": "1"})

try:
    response = urllib.request.urlopen(req, timeout=10)
    metaint = int(response.headers.get("icy-metaint", 0))
    print(f"icy-metaint: {metaint}")

    if metaint > 0:
        response.read(metaint)
        meta_byte = response.read(1)
        if meta_byte:
            meta_len = ord(meta_byte) * 16
            if meta_len > 0:
                meta_data = response.read(meta_len)
                print(f"Metadata: {meta_data.decode('utf-8', errors='ignore')}")
            else:
                print("Empty metadata block.")
        else:
            print("Stream ended unexpectedly.")
    else:
        print("No icy-metaint header returned.")
except Exception as e:
    print(f"Error: {e}")
