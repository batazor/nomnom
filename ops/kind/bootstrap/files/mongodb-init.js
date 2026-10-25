// ADR 8, ADR 31: replica set rs0 и пользователь restaurants MongoDB тестового окружения.
// Выполняется Job mongodb-restaurants-init (templates/mongodb.yaml) под root; идемпотентно.
try {
  rs.status()
} catch (e) {
  rs.initiate({ _id: 'rs0', members: [{ _id: 0, host: process.env.MONGODB_MEMBER }] })
}
while (!db.hello().isWritablePrimary) {
  sleep(500)
}
const app = db.getSiblingDB('restaurants')
const pwd = process.env.RESTAURANTS_PASSWORD
const roles = [{ role: 'readWrite', db: 'restaurants' }, { role: 'dbAdmin', db: 'restaurants' }]
if (app.getUser('restaurants')) {
  app.updateUser('restaurants', { pwd, roles })
} else {
  app.createUser({ user: 'restaurants', pwd, roles })
}
print('mongodb-restaurants replica set initiated')
