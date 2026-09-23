package com.acme.text;

import java.util.Locale;

/** Old implementation retained after shared-text moved to its own repository. */
public final class TextUtil {
    public static String normalize(String value) {
        return value.trim().toLowerCase(Locale.ROOT);
    }
}
