"""Base of the ``spec`` sections of the configuration file (ADR 17)."""

from pydantic import BaseModel, ConfigDict
from pydantic.alias_generators import to_camel


class Section(BaseModel):
    """Section of ``spec``: keys are the camelCase field names, an unknown key is an error.

    The file is validated by alias only (``parse_spec``); code and tests build sections
    by field name.
    """

    model_config = ConfigDict(
        extra="forbid",
        alias_generator=to_camel,
        validate_by_alias=True,
        validate_by_name=True,
    )
