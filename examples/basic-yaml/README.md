# basic-yaml: the basic pipeline in Pulumi YAML

`Pulumi.yaml` is `examples/basic` written in Pulumi YAML. It declares the same sources, stages, gate, jobs, artifact and Stacks group.

Write stage entry rules as object literals with a `kind` (`approval`, `manual`, `http`, `all`, `any`) and the arguments of the function of the same name. Do not call the rule functions with `fn::invoke`. Their return type, `delivery:index:Rule`, is recursive, and the Pulumi YAML type checker overflows its stack on it (`typeCache.isAssignable`), even for a single `manual` rule.
