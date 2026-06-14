#Toat astea care ne zic chestii depsre aplicatie
urls=("pro-fm-poller.fly.dev" "https://fly.io/apps/pro-fm-poller" "https://app.codecov.io/gh/viktorashi/follow-pro-fm" "https://github.com/viktorashi/follow-pro-fm/actions/workflows/ci.yml" "https://resend.com/emails")

for url in "${urls[@]}"; do
  open "$url"
done
