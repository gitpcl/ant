<?php

namespace App;

// ant:pint-format the class below has trailing-whitespace drift the formatter fixes
class Service
{
    public function handle(string $name): string
    {
        return trim($name);   
    }
}
