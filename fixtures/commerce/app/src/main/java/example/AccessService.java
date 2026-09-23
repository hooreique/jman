package example;

import com.acme.text.TextUtil;

public final class AccessService {
    public boolean isAdmin(String name) {
        return TextUtil.normalize(name).equals("admin");
    }

    public static void main(String[] args) {
        System.out.println(new AccessService().isAdmin(args[0]));
    }
}
