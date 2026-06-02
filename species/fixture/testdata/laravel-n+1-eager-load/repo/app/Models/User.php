<?php

namespace App\Models;

// User is a minimal Eloquent-style stand-in for the laravel-n+1-eager-load
// fixture: the species targets the lazy relation access in Report::build(), not
// this model. It is here only so the fixture is a valid, parseable PHP project.
class User
{
    public static function all(): array
    {
        return [];
    }

    public static function with(string $relation): self
    {
        return new self();
    }

    public function get(): array
    {
        return [];
    }
}
