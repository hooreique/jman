package example;

import org.springframework.aop.framework.ProxyFactory;
import org.aopalliance.intercept.MethodInterceptor;
import org.springframework.context.annotation.AnnotationConfigApplicationContext;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

public class StackCheck {
    public interface Greeting { String greet(); String outer(); }
    public static class Service implements Greeting {
        public String greet() { return "hello"; }
        public String outer() { return greet(); }
    }
    @Configuration
    public static class Config {
        @Bean public Greeting greeting() {
            ProxyFactory factory = new ProxyFactory(new Service());
            factory.addAdvice((MethodInterceptor) call -> call.getMethod().getName().equals("greet") ? "advised" : call.proceed());
            return (Greeting) factory.getProxy();
        }
    }
    public static void main(String[] args) {
        Navigation navigation = new Navigation();
        Person person = navigation.lombokBuilder();
        if (!navigation.lombokGetter(person).equals("Alice")) throw new AssertionError("Lombok");
        if (!navigation.mapping(person).name.equals("Alice")) throw new AssertionError("MapStruct");
        if (!navigation.querydsl().equals("person.name")) throw new AssertionError("QueryDSL");
        try (var context = new AnnotationConfigApplicationContext(Config.class)) {
            Greeting proxy = context.getBean(Greeting.class);
            if (!proxy.greet().equals("advised")) throw new AssertionError("proxy advice");
            if (!proxy.outer().equals("hello")) throw new AssertionError("self-invocation must bypass proxy");
        }
        System.out.println("PASS Lombok, MapStruct, QueryDSL/JPA, Spring context and AOP self-invocation");
    }
}
