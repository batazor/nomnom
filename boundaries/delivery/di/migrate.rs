//! Composition root процесса `delivery-migrate` (`cmd/delivery-migrate`):
//! применяет миграции PostgreSQL и завершается (ADR 31). Соединение живёт
//! только на время одного применения миграций.

use shaku::{Component, HasComponent, Interface, module};

use crate::infrastructure::postgres;
use crate::pkg::config::DatabaseConfig;

/// Применение схемы хранилища.
pub trait SchemaMigration: Interface {
    /// Применяет ожидающие миграции; возвращает версии, применённые этим запуском.
    fn apply(&self) -> Result<Vec<i64>, String>;
}

#[derive(Component)]
#[shaku(interface = SchemaMigration)]
pub struct PostgresMigration {
    database: DatabaseConfig,
}

impl SchemaMigration for PostgresMigration {
    fn apply(&self) -> Result<Vec<i64>, String> {
        postgres::migrate(&self.database)
    }
}

module! {
    pub MigrateModule {
        components = [PostgresMigration],
        providers = []
    }
}

impl MigrateModule {
    pub fn migrate(&self) -> Result<Vec<i64>, String> {
        HasComponent::<dyn SchemaMigration>::resolve_ref(self).apply()
    }
}

/// Собирает граф `delivery-migrate`. Пустой `DATABASE__URL` — ошибка
/// применения, а не сборки: проверку делает [`postgres::migrate`].
#[must_use]
pub fn compose(database: DatabaseConfig) -> MigrateModule {
    MigrateModule::builder()
        .with_component_parameters::<PostgresMigration>(PostgresMigrationParameters { database })
        .build()
}
