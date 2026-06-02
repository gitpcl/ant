<?php

namespace App\Repositories;

// UserRepository is a hermetic fixture for laravel-raw-where-concat. active()
// builds a raw WHERE by concatenating $status into the SQL text —
// ->whereRaw("status = " . $status) — the SQL-injection smell: the value is parsed
// as SQL, not bound as data. recent() in this fixture uses a BOUND parameter
// (->whereRaw("created_at > ?", [$since])) and a STATIC raw fragment
// (->whereRaw("deleted_at is null")): NEITHER concatenates a value, so the
// detector leaves them alone — proving the bound-vs-concatenated discrimination.
class UserRepository
{
    public function active($query, $status)
    {
        return $query->whereRaw("status = " . $status)->get();
    }

    public function recent($query, $since)
    {
        return $query
            ->whereRaw("created_at > ?", [$since])
            ->whereRaw("deleted_at is null")
            ->get();
    }
}
