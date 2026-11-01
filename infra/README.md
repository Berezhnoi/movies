# Lab runbook (from scratch)

Windows host: VirtualBox + Vagrant. Everything else runs inside the VM.

## 1. VM (Windows)

```powershell
cd infra\vm
vagrant up        # Ubuntu 24.04 + Docker, Go, Terraform, aws cli, K3s
vagrant ssh
```

## 2. Code (VM)

```sh
git clone https://github.com/Berezhnoi/movies.git ~/movies
cd ~/movies
```

## 3. Deploy everything (VM)

```sh
make -C infra secrets   # random lab secrets -> infra/secrets.env (gitignored)
make -C infra all       # Floci -> Lambda build+test -> terraform apply -> image -> K3s deploy
```

## 4. Check (VM)

```sh
make -C infra k3s-status   # node Ready, pods Running
make -C infra e2e          # admin creates a user -> email found in Floci SES
make -C infra mailbox      # show captured emails
```

## 5. Use the app (Windows)

```powershell
vagrant ssh -- -L 30080:localhost:30080   # keep open
```

Open `http://localhost:30080/login` and log in as `admin@movies.local`. The password is in the VM:
`grep ^ADMIN_PASSWORD= ~/movies/infra/secrets.env | cut -d= -f2-`

## Teardown

```sh
make -C infra tf-destroy && kubectl delete ns movies && make -C infra floci-down   # VM
vagrant destroy -f                                                                 # Windows
```

## Notes

- **Versions:** K3s `v1.36.5+k3s1` (bundled Traefik and local-path storage) and Floci `floci/floci:2.2.0`.
- **Ports:** inside the VM, the app NodePort is 30080, Traefik uses 80/443 and Floci uses 4566. VirtualBox
  forwards host 8080 to guest 80 and host 4566 to guest 4566, both bound to `127.0.0.1`.
- **Function URL DNS:** Floci returns `http://<id>.lambda-url.us-east-1.floci:4566/`, which no DNS server
  resolves. Floci routes these requests by the `Host` header, so a CoreDNS custom zone
  (`k8s/coredns-custom.yaml.tmpl`) points `*.floci` at the node IP. The app uses the URL unchanged.
- **Cold start:** the first Lambda invoke pulls the runtime image and takes about 20 seconds, so K3s sets
  `WEBHOOK_TIMEOUT=60s`.
- **Images:** K3s runs its own containerd, separate from Docker. Images built with Docker are imported with
  `docker save … | sudo k3s ctr images import -`.
- **Secrets:** `secrets.env`, Terraform state and kubeconfig files are gitignored. Never commit them,
  because the state includes the webhook secret.
- `make -C infra k3s-smoke` checks ingress, a PVC that persists across pod restarts, and that Docker and
  Floci still work alongside K3s.
