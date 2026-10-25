#!/usr/bin/env bash
# ADR 8, ADR 31: секреты MongoDB restaurants. Режим "secrets" создаёт root-пароль, пароль
# restaurants и keyFile (Secret mongodb-restaurants в nomnom-data) и URI для приложения
# (Secret restaurants-mongodb в nomnom-restaurants); существующие значения сохраняются.
# Режим "init" после старта Pod идемпотентно выполняет rs.initiate и создаёт пользователя
# restaurants (readWrite и dbAdmin: restaurants-migrate создаёт коллекции и индексы).
set -euo pipefail
context="${1:?usage: mongodb.sh <kube-context> secrets|init}"
mode="${2:?usage: mongodb.sh <kube-context> secrets|init}"
k() { kubectl --context "${context}" "$@"; }
host="mongodb-restaurants.nomnom-data.svc.cluster.local:27017"

field() { k -n nomnom-data get secret mongodb-restaurants -o jsonpath="{.data.$1}" 2>/dev/null | base64 -d || true; }
root_password="$(field root-password)"
password="$(field password)"

case "${mode}" in
  secrets)
    keyfile="$(field keyfile)"
    [[ -n "${root_password}" ]] || root_password="$(openssl rand -hex 24)"
    [[ -n "${password}" ]] || password="$(openssl rand -hex 24)"
    [[ -n "${keyfile}" ]] || keyfile="$(openssl rand -base64 756 | tr -d '\n')"
    k -n nomnom-data create secret generic mongodb-restaurants \
      --from-literal=root-password="${root_password}" --from-literal=password="${password}" \
      --from-literal=keyfile="${keyfile}" --dry-run=client -o yaml | k apply -f - >/dev/null
    uri="mongodb://restaurants:${password}@${host}/restaurants?replicaSet=rs0&authSource=restaurants"
    k -n nomnom-restaurants create secret generic restaurants-mongodb --from-literal=uri="${uri}" \
      --dry-run=client -o yaml | k apply -f - >/dev/null
    echo "mongodb-restaurants credentials published"
    ;;
  init)
    k -n nomnom-data exec deploy/mongodb-restaurants -c mongodb -- mongosh --quiet \
      -u root -p "${root_password}" --authenticationDatabase admin --eval "
        try { rs.status() } catch (e) {
          rs.initiate({ _id: 'rs0', members: [{ _id: 0, host: '${host}' }] })
        }
        while (!db.hello().isWritablePrimary) { sleep(500) }
        const app = db.getSiblingDB('restaurants')
        const roles = [{ role: 'readWrite', db: 'restaurants' }, { role: 'dbAdmin', db: 'restaurants' }]
        if (app.getUser('restaurants')) { app.updateUser('restaurants', { pwd: '${password}', roles }) }
        else { app.createUser({ user: 'restaurants', pwd: '${password}', roles }) }
      " >/dev/null
    echo "mongodb-restaurants replica set initiated"
    ;;
  *) echo "unknown mode ${mode}" >&2; exit 2 ;;
esac
