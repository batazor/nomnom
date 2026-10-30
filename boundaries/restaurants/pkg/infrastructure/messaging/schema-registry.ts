// Subjects id схемы из Confluent-совместимого Schema Registry (FR-715): subject
// Integration Event — его event_type (FR-717). Id схемы неизменяемы, поэтому
// найденное соответствие хранится до конца жизни процесса.

/** Id схемы неизвестен Registry: запись не прочитать никаким повтором. */
export class UnknownSchemaError extends Error {
  constructor(schemaId: number) {
    super(`schema id ${schemaId} is unknown to the schema registry`);
    this.name = "UnknownSchemaError";
  }
}

export interface SchemaSubjects {
  /**
   * `UnknownSchemaError` — id неизвестен; любая другая ошибка transient:
   * Registry недоступен.
   */
  subjects(schemaId: number): Promise<readonly string[]>;
}

export class HttpSchemaRegistry implements SchemaSubjects {
  readonly #url: URL;
  readonly #timeoutMs: number;
  readonly #known = new Map<number, readonly string[]>();

  constructor(options: { url: URL; timeoutMs: number }) {
    this.#url = options.url;
    this.#timeoutMs = options.timeoutMs;
  }

  async subjects(schemaId: number): Promise<readonly string[]> {
    const known = this.#known.get(schemaId);
    if (known !== undefined) return known;
    const response = await fetch(
      new URL(`schemas/ids/${schemaId}/versions`, this.#url),
      { signal: AbortSignal.timeout(this.#timeoutMs) },
    );
    if (response.status === 404) throw new UnknownSchemaError(schemaId);
    if (!response.ok) {
      throw new Error(
        `schema registry: subjects of schema ${schemaId}: status ${response.status}: ${await response.text()}`,
      );
    }
    const versions = (await response.json()) as ReadonlyArray<{
      subject: string;
    }>;
    const subjects = Object.freeze(versions.map((version) => version.subject));
    this.#known.set(schemaId, subjects);
    return subjects;
  }
}
